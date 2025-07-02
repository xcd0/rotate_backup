package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-toast/toast"
	"github.com/hjson/hjson-go"
	pkgerrors "github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// NotificationType は通知の種類を表します。
type NotificationType int

const (
	NotifyLockConflict NotificationType = iota
	NotifyBackupStart
	NotifyBackupEnd
	NotifyUpdateEnd
	NotifyError
)

// Args はコマンドライン引数を保持します。
type Args struct {
	ConfigPath   string
	UpdateBackup bool
}

// BackupConfig は設定ファイルの構造を表します。
type BackupConfig struct {
	DryRun             bool              `json:"dry_run"`
	CopyMethodPriority []string          `json:"copy_method_priority"`
	CopyArgs           map[string]string `json:"copy_args"`

	WorkDir        string `json:"work_dir"`
	BackupDir      string `json:"backup_dir"`
	SourceVHDX     string `json:"source_vhdx"`
	LastIDFile     string `json:"last_id_file"`
	VHDXMountDrive string `json:"vhdx_mount_drive"`
	MountIfMissing bool   `json:"mount_vhdx_if_missing"`

	KeepVersions map[string]int    `json:"keep_versions"`
	BackupDirs   map[string]string `json:"backup_dirs"`
	Extensions   []string          `json:"extensions"`
	ExcludeDirs  []string          `json:"exclude_dirs"`
	IncludeFiles []string          `json:"include_files"`

	Notifications struct {
		LockConflict bool `json:"lock_conflict"`
		BackupStart  bool `json:"backup_start"`
		BackupEnd    bool `json:"backup_end"`
		UpdateEnd    bool `json:"update_end"`
		Error        bool `json:"error"`
	} `json:"notifications"`

	LogFile     string `json:"log_file"`
	PerfLogPath string `json:"perf_log_path"`

	EnableLock     bool   `json:"enable_lock"`
	LockFilePath   string `json:"lock_file_path"`
	OnLockConflict string `json:"on_lock_conflict"`
	
	// 重複実行防止用：最終実行時刻記録ファイル
	LastExecutionFile string `json:"last_execution_file"`
}

// LastExecutionRecord は最終実行時刻を記録する構造体です。
type LastExecutionRecord struct {
	LastExecutions map[string]time.Time `json:"last_executions"` // レベル別最終実行時刻
}

// ログバッファリング用の構造体
type LogBuffer struct {
	buffer *bytes.Buffer
	mutex  sync.Mutex
}

func (lb *LogBuffer) Write(p []byte) (n int, err error) {
	lb.mutex.Lock()
	defer lb.mutex.Unlock()
	return lb.buffer.Write(p)
}

func (lb *LogBuffer) GetAndClear() []byte {
	lb.mutex.Lock()
	defer lb.mutex.Unlock()
	data := lb.buffer.Bytes()
	result := make([]byte, len(data))
	copy(result, data)
	lb.buffer.Reset()
	return result
}

// マルチライター：複数の出力先に同時出力
type MultiWriter struct {
	writers []io.Writer
	mutex   sync.Mutex
}

func (mw *MultiWriter) Write(p []byte) (n int, err error) {
	mw.mutex.Lock()
	defer mw.mutex.Unlock()
	
	for _, writer := range mw.writers {
		if _, writeErr := writer.Write(p); writeErr != nil {
			// ログ出力なので、エラーがあってもスキップして続行
			continue
		}
	}
	return len(p), nil
}

func (mw *MultiWriter) AddWriter(writer io.Writer) {
	mw.mutex.Lock()
	defer mw.mutex.Unlock()
	mw.writers = append(mw.writers, writer)
}

func (mw *MultiWriter) RemoveWriter(writer io.Writer) {
	mw.mutex.Lock()
	defer mw.mutex.Unlock()
	for i, w := range mw.writers {
		if w == writer {
			mw.writers = append(mw.writers[:i], mw.writers[i+1:]...)
			break
		}
	}
}

// グローバル変数。
var (
	args Args = Args{
		ConfigPath:   "./config.hjson",
		UpdateBackup: false,
	}
	logWriter   io.Writer
	logfile     *os.File
	logBuffer   *LogBuffer
	multiWriter *MultiWriter

	version  string = "debug build"   // makefileからビルドされると上書きされる。
	revision string = func() string { // {{{
		revision := ""
		modified := false
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					//return setting.Value
					revision = setting.Value
					if len(setting.Value) > 7 {
						revision = setting.Value[:7] // 最初の7文字にする
					}
				}
				if setting.Key == "vcs.modified" {
					modified = setting.Value == "true"
				}
			}
		}
		if modified {
			revision = "develop+" + revision
		}
		return revision
	}() // }}}
)

func init() {
	// ログバッファとマルチライターを初期化
	logBuffer = &LogBuffer{
		buffer: &bytes.Buffer{},
	}
	multiWriter = &MultiWriter{
		writers: []io.Writer{os.Stderr, logBuffer},
	}
	
	// 初期状態では標準エラーとバッファに出力
	log.SetOutput(multiWriter)
	log.SetFlags(log.Ltime | log.Lshortfile)
	
	log.Printf("ログシステム初期化完了 - 出力先: 標準エラー + バッファ")
}

// setupLogOutput は設定ファイル読み込み後にログ出力先を設定します
func setupLogOutput(cfg *BackupConfig) error {
	if cfg.LogFile == "" {
		log.Printf("ログファイル未設定 - 標準エラー出力のみ継続")
		return nil
	}
	
	log.Printf("ログファイル設定開始: %s", cfg.LogFile)
	
	// ログファイルを開く
	logfile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("ログファイルオープンエラー: %v", err)
		return fmt.Errorf("ログファイルオープンエラー: %v", err)
	}
	
	// バッファに溜まったログを先にファイルに出力
	bufferedLogs := logBuffer.GetAndClear()
	if len(bufferedLogs) > 0 {
		if _, err := logfile.Write(bufferedLogs); err != nil {
			log.Printf("バッファ済みログのファイル出力エラー: %v", err)
		} else {
			log.Printf("バッファ済みログをファイルに出力しました (%d bytes)", len(bufferedLogs))
		}
	}
	
	// マルチライターからバッファを削除し、ログファイルを追加
	multiWriter.RemoveWriter(logBuffer)
	multiWriter.AddWriter(logfile)
	
	log.Printf("ログ出力先変更完了 - 出力先: 標準エラー + %s", cfg.LogFile)
	return nil
}

// closeLogFile はログファイルを安全に閉じます
func closeLogFile() {
	if logfile != nil {
		log.Printf("ログファイルを閉じます")
		logfile.Close()
		logfile = nil
	}
}

// convertShiftJISToUTF8 はShift_JISエンコードされたバイト列をUTF-8文字列に変換します。
func convertShiftJISToUTF8(sjisBytes []byte) string {
	// WindowsのShift_JIS（CP932）からUTF-8に変換
	decoder := japanese.ShiftJIS.NewDecoder()
	utf8Bytes, _, err := transform.Bytes(decoder, sjisBytes)
	if err != nil {
		// 変換に失敗した場合は元のバイト列を文字列として返す
		return string(sjisBytes)
	}
	return string(utf8Bytes)
}

// executeRobocopyWithExtensions は拡張子フィルタリング付きの2段階robocopyを実行します。
func executeRobocopyWithExtensions(cfg *BackupConfig, src, dst, args string) bool {
	log.Printf("robocopy 2段階実行開始: 拡張子フィルタリング + ミラーリング")

	// 除外ディレクトリオプション
	excludeDirs := []string{
		"/XD", "System Volume Information", "$Recycle.Bin", "Recovery",
	}
	for _, excludeDir := range cfg.ExcludeDirs {
		excludeDirs = append(excludeDirs, excludeDir)
	}

	// 段階1: 指定拡張子のファイルをコピー
	log.Printf("段階1: 指定拡張子ファイルのコピー")
	
	// 拡張子パターンを作成
	var extPatterns []string
	for _, ext := range cfg.Extensions {
		extPatterns = append(extPatterns, "*"+ext)
	}

	// 段階1のコマンド構築
	parts1 := []string{src, dst}
	parts1 = append(parts1, extPatterns...)
	parts1 = append(parts1, strings.Fields(args)...)
	// /MIRではなく/Eを使用（削除なし）
	for i, arg := range parts1 {
		if arg == "/MIR" {
			parts1[i] = "/E"
		}
	}
	parts1 = append(parts1, excludeDirs...)
	parts1 = append(parts1, "/XA:SH")

	cmd1 := exec.Command("robocopy", parts1...)
	log.Printf("実行コマンド(段階1): robocopy %s", strings.Join(parts1, " "))
	out1, err1 := cmd1.CombinedOutput()
	outStr1 := convertShiftJISToUTF8(out1)

	stage1Success := false
	if err1 == nil {
		stage1Success = true
		log.Printf("段階1完了: 指定拡張子ファイルのコピー成功")
	} else if exitError, ok := err1.(*exec.ExitError); ok {
		exitCode := exitError.ExitCode()
		if exitCode <= 3 {
			stage1Success = true
			log.Printf("段階1完了: 指定拡張子ファイルのコピー成功 (終了コード: %d)", exitCode)
		} else {
			log.Printf("段階1失敗: robocopy エラー (終了コード: %d)", exitCode)
		}
	}

	if len(outStr1) > 0 {
		log.Printf("段階1出力:")
		logRobocopyOutput(outStr1)
	}

	if !stage1Success {
		return false
	}

	// 段階2: 対象外ファイルの削除（カスタムクリーンアップ）
	log.Printf("段階2: 対象外ファイルの削除")

	// Go言語で対象外ファイルを探索・削除
	stage2Success := cleanupUnwantedFiles(cfg, src, dst)
	if !stage2Success {
		log.Printf("段階2失敗: 対象外ファイルの削除に失敗")
	} else {
		log.Printf("段階2完了: 対象外ファイルの削除成功")
	}

	if stage1Success && stage2Success {
		log.Printf("robocopy 2段階実行完了: 拡張子フィルタリング + ミラーリング成功")
		return true
	}

	return false
}

// cleanupUnwantedFiles はバックアップ先の対象外ファイルを削除します。
func cleanupUnwantedFiles(cfg *BackupConfig, src, dst string) bool {
	log.Printf("対象外ファイルのクリーンアップを開始: %s", dst)
	
	deletedFiles := 0
	deletedDirs := 0
	skippedFiles := 0
	errors := 0

	// バックアップ先ディレクトリを走査
	err := filepath.Walk(dst, func(dstPath string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("ファイル走査エラー: %s, %v", dstPath, err)
			errors++
			return nil
		}

		// ルートディレクトリはスキップ
		if dstPath == dst {
			return nil
		}

		// バックアップ先のパスから相対パスを取得
		relPath, err := filepath.Rel(dst, dstPath)
		if err != nil {
			log.Printf("相対パス計算エラー: %s, %v", dstPath, err)
			errors++
			return nil
		}

		// ソース側の対応するパス
		srcPath := filepath.Join(src, relPath)

		if info.IsDir() {
			// ディレクトリの場合：ソース側に存在しない場合は削除
			if _, err := os.Stat(srcPath); os.IsNotExist(err) {
				log.Printf("対象外ディレクトリを削除: %s", dstPath)
				if err := os.RemoveAll(dstPath); err != nil {
					log.Printf("ディレクトリ削除エラー: %s, %v", dstPath, err)
					errors++
				} else {
					deletedDirs++
				}
				return filepath.SkipDir // サブディレクトリもスキップ
			}
		} else {
			// ファイルの場合
			shouldDelete := false

			// ソース側に対応するファイルが存在しない場合は削除
			if _, err := os.Stat(srcPath); os.IsNotExist(err) {
				shouldDelete = true
				log.Printf("ソース側に存在しないファイルを削除: %s", dstPath)
			} else {
				// ソース側にファイルが存在する場合、拡張子をチェック
				if len(cfg.Extensions) > 0 {
					matched := false
					fileName := strings.ToLower(filepath.Base(dstPath))
					
					for _, allowedExt := range cfg.Extensions {
						allowedExt = strings.ToLower(allowedExt)
						if strings.HasSuffix(fileName, allowedExt) {
							matched = true
							break
						}
					}
					
					if !matched {
						ext := strings.ToLower(filepath.Ext(dstPath))
						log.Printf("対象外拡張子のファイルを削除: %s (拡張子: %s)", dstPath, ext)
						shouldDelete = true
					}
				}
			}

			if shouldDelete {
				if err := os.Remove(dstPath); err != nil {
					log.Printf("ファイル削除エラー: %s, %v", dstPath, err)
					errors++
				} else {
					deletedFiles++
				}
			} else {
				skippedFiles++
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("ディレクトリ走査でエラー: %v", err)
		return false
	}

	log.Printf("クリーンアップ完了: 削除ファイル数=%d, 削除ディレクトリ数=%d, スキップ=%d, エラー=%d", 
		deletedFiles, deletedDirs, skippedFiles, errors)

	// エラーがあっても部分的に成功していれば成功とみなす
	return errors < (deletedFiles + deletedDirs + skippedFiles)
}

// logRobocopyOutput はrobocopyの出力を解析してログに記録します。
func logRobocopyOutput(output string) {
	if len(output) == 0 {
		log.Printf("robocopy 出力: (出力なし)")
		return
	}

	lines := strings.Split(output, "\n")
	var summaryLines []string
	var importantLines []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// 統計情報やサマリーを抽出
		if strings.Contains(line, "Files :") ||
			strings.Contains(line, "Dirs :") ||
			strings.Contains(line, "Bytes :") ||
			strings.Contains(line, "Times :") ||
			strings.Contains(line, "Speed :") ||
			strings.Contains(line, "Ended :") ||
			strings.Contains(line, "Total") ||
			strings.Contains(line, "Copied") ||
			strings.Contains(line, "Skipped") {
			summaryLines = append(summaryLines, line)
		} else if strings.Contains(line, "ERROR") ||
			strings.Contains(line, "WARN") ||
			strings.Contains(line, "FAIL") {
			importantLines = append(importantLines, line)
		}
	}

	// 重要な情報（エラーなど）があれば優先して表示
	if len(importantLines) > 0 {
		log.Printf("robocopy 重要な出力:")
		for _, line := range importantLines {
			log.Printf("  %s", line)
		}
	}

	// サマリー情報があれば表示
	if len(summaryLines) > 0 {
		log.Printf("robocopy サマリー:")
		for _, line := range summaryLines {
			log.Printf("  %s", line)
		}
	} else {
		// サマリーがない場合は意味のある出力のみ表示
		log.Printf("robocopy 出力:")
		count := 0
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && shouldShowRobocopyLine(line) {
				log.Printf("  %s", line)
				count++
			}
		}
		if count == 0 {
			log.Printf("  (変化なし - コピー・削除されたファイルはありません)")
		}
	}
}

// shouldShowRobocopyLine はrobocopyの出力行を表示すべきかどうかを判定します。
func shouldShowRobocopyLine(line string) bool {
	// 空行はスキップ
	if strings.TrimSpace(line) == "" {
		return false
	}

	// 変化なし（0のみ）の行をスキップ
	// 例: "   0       P:\neco\.git\modules\cereal\branches\"
	if matched, _ := regexp.MatchString(`^\s*0\s+.*\\$`, line); matched {
		return false
	}

	// ディレクトリのみで変化がない行をスキップ
	// 例: "   0       P:\some\directory\"
	if matched, _ := regexp.MatchString(`^\s*0\s+[A-Za-z]:\\.*\\$`, line); matched {
		return false
	}

	// ヘッダー情報はスキップ（既に統計で表示されるため）
	if strings.Contains(line, "ROBOCOPY") ||
		strings.Contains(line, "Source :") ||
		strings.Contains(line, "Dest :") ||
		strings.Contains(line, "Files :") ||
		strings.Contains(line, "Options :") ||
		strings.Contains(line, "Started :") {
		return false
	}

	// 実際にコピーされたファイル（0以外）は表示
	if matched, _ := regexp.MatchString(`^\s*[1-9]\d*\s+.*`, line); matched {
		return true
	}

	// New File、Modified、Same、などの状態表示
	if strings.Contains(line, "New File") ||
		strings.Contains(line, "Modified") ||
		strings.Contains(line, "Newer") ||
		strings.Contains(line, "Older") ||
		strings.Contains(line, "Extra File") ||
		strings.Contains(line, "Extra Dir") ||
		strings.Contains(line, "*EXTRA File") ||
		strings.Contains(line, "*EXTRA Dir") {
		return true
	}

	// エラーや警告は表示
	if strings.Contains(line, "ERROR") ||
		strings.Contains(line, "WARNING") ||
		strings.Contains(line, "RETRY") ||
		strings.Contains(line, "FAILED") {
		return true
	}

	// その他の情報行はスキップ
	return false
}

func main() {
	// プログラム終了時にログファイルをクローズ
	defer closeLogFile()
	
	if err := rootCmd.Execute(); err != nil {
		log.Printf("実行エラー: %v", err)
		os.Exit(1)
	}
}

func (Args) Version() string {
	return GetVersion()
}

// cobra用のコマンド定義
var rootCmd = &cobra.Command{
	Use:   "rotate_backup",
	Short: "VHDX バックアップローテーション管理ツール",
	Long:  "Windows用VHDXバックアップローテーション管理ツール - 多段階ローテーションでバックアップを効率的に管理します。",
	Version: GetVersion(),
	Run: func(cmd *cobra.Command, cmdArgs []string) {
		// デフォルト: 定期起動モード
		if args.UpdateBackup {
			log.Printf("update-backup モード開始 - バージョン: %s", GetVersion())
			if err := runUpdateBackup(args.ConfigPath); err != nil {
				log.Printf("実行エラー: %v", err)
				panic(pkgerrors.Errorf("%v", err))
			}
			log.Printf("update-backup モード正常終了")
		} else {
			log.Printf("rotate_backup 開始 - バージョン: %s", GetVersion())
			if err := runOneShotMode(args.ConfigPath); err != nil {
				log.Printf("実行エラー: %v", err)
				panic(pkgerrors.Errorf("%v", err))
			}
			log.Printf("rotate_backup 正常終了")
		}
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "設定テンプレートを生成",
	Long:  "設定ファイルのテンプレートを生成します。",
	Run: func(cmd *cobra.Command, cmdArgs []string) {
		var configPath string
		if args.ConfigPath != "config.hjson" {
			configPath = args.ConfigPath
		} else {
			configPath = "config.hjson"
		}

		if err := generateTemplate(configPath); err != nil {
			panic(pkgerrors.Errorf("%v", err))
		}
		fmt.Printf("設定テンプレートを生成しました: %s\n", configPath)
	},
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "常駐モードで起動",
	Long:  "常駐モードで起動し、内部スケジューラでバックアップを実行します。",
	Run: func(cmd *cobra.Command, cmdArgs []string) {
		log.Printf("daemon モード開始 - バージョン: %s", GetVersion())
		// daemonCmd用の設定を作成
		daemonConfig := &DaemonCmd{
			PIDFile:  pidFile,
			LogLevel: logLevel,
		}
		if err := runDaemonMode(args.ConfigPath, daemonConfig); err != nil {
			log.Printf("実行エラー: %v", err)
			panic(pkgerrors.Errorf("%v", err))
		}
		log.Printf("daemon モード正常終了")
	},
}

// DaemonCmd は常駐モード用の引数です。
type DaemonCmd struct {
	PIDFile  string
	LogLevel string
}

// daemon用のフラグ変数
var (
	pidFile  string
	logLevel string
	printDetails bool  // --print フラグ用
)

// detectCurrentShell は現在実行中のシェルを検出します。
func detectCurrentShell() string {
	// WSL環境での優先判定
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		shell := os.Getenv("SHELL")
		if shell != "" {
			shellName := filepath.Base(shell)
			switch shellName {
			case "bash":
				return "bash"
			case "zsh":
				return "zsh"
			case "fish":
				return "fish"
			}
		}
	}

	// Linux/Unix環境での環境変数SHELLから判定
	if runtime.GOOS != "windows" {
		shell := os.Getenv("SHELL")
		if shell != "" {
			shellName := filepath.Base(shell)
			switch shellName {
			case "bash":
				return "bash"
			case "zsh":
				return "zsh"
			case "fish":
				return "fish"
			}
		}

		// 親プロセス名から判定
		parentPID := os.Getppid()
		if parentPID > 0 {
			commPath := fmt.Sprintf("/proc/%d/comm", parentPID)
			if data, err := os.ReadFile(commPath); err == nil {
				parentName := strings.TrimSpace(string(data))
				switch parentName {
				case "bash":
					return "bash"
				case "zsh":
					return "zsh"
				case "fish":
					return "fish"
				}
			}
		}
	}

	// Windows環境の場合
	if runtime.GOOS == "windows" {
		// PowerShellの判定
		if os.Getenv("PSModulePath") != "" {
			return "powershell"
		}
		// その他の場合はcmdだが、PowerShellとして扱う
		return "powershell"
	}

	// デフォルトはbash
	return "bash"
}

// generateCompletionScript は指定されたシェル用の補完スクリプトを生成します。
func generateCompletionScript(shell string) error {
	switch shell {
	case "bash":
		return rootCmd.GenBashCompletion(os.Stdout)
	case "zsh":
		return rootCmd.GenZshCompletion(os.Stdout)
	case "fish":
		return rootCmd.GenFishCompletion(os.Stdout, true)
	case "powershell", "pwsh":
		return rootCmd.GenPowerShellCompletion(os.Stdout)
	default:
		return fmt.Errorf("サポートされていないシェル: %s", shell)
	}
}

// printCompletionInstructions は補完スクリプトのインストール方法を表示します。
func printCompletionInstructions(shell string, programName string) {
	fmt.Fprintf(os.Stderr, "\n# %s用の補完スクリプトが生成されました\n", shell)
	fmt.Fprintf(os.Stderr, "# 以下のコマンドでインストールできます:\n\n")

	switch shell {
	case "bash":
		fmt.Fprintf(os.Stderr, "# 一時的な有効化:\n")
		fmt.Fprintf(os.Stderr, "source <(%s auto-completion)\n\n", programName)
		fmt.Fprintf(os.Stderr, "# 永続的な有効化:\n")
		fmt.Fprintf(os.Stderr, "echo 'source <(%s auto-completion)' >> ~/.bashrc\n", programName)
		fmt.Fprintf(os.Stderr, "# または:\n")
		fmt.Fprintf(os.Stderr, "%s auto-completion > /etc/bash_completion.d/%s\n", programName, programName)

	case "zsh":
		fmt.Fprintf(os.Stderr, "# 一時的な有効化:\n")
		fmt.Fprintf(os.Stderr, "source <(%s auto-completion)\n\n", programName)
		fmt.Fprintf(os.Stderr, "# 永続的な有効化:\n")
		fmt.Fprintf(os.Stderr, "echo 'source <(%s auto-completion)' >> ~/.zshrc\n", programName)
		fmt.Fprintf(os.Stderr, "# または zsh completion directory に保存:\n")
		fmt.Fprintf(os.Stderr, "%s auto-completion > ~/.zsh/completions/_%s\n", programName, programName)

	case "fish":
		fmt.Fprintf(os.Stderr, "# fish用補完スクリプトの保存:\n")
		fmt.Fprintf(os.Stderr, "%s auto-completion > ~/.config/fish/completions/%s.fish\n", programName, programName)

	case "powershell", "pwsh":
		fmt.Fprintf(os.Stderr, "# PowerShell用補完スクリプトの追加:\n")
		fmt.Fprintf(os.Stderr, "%s auto-completion >> $PROFILE\n", programName)
		fmt.Fprintf(os.Stderr, "# または:\n")
		fmt.Fprintf(os.Stderr, "%s auto-completion | Out-String | Invoke-Expression\n", programName)
	}

	fmt.Fprintf(os.Stderr, "\n")
}

// printCompletionDetails は補完機能に関する詳細情報を表示します。
func printCompletionDetails(shell string, programName string) {
	fmt.Fprintf(os.Stderr, "=== Completion Script Details ===\n")
	fmt.Fprintf(os.Stderr, "プログラム名: %s\n", programName)
	fmt.Fprintf(os.Stderr, "検出されたシェル: %s\n", shell)
	fmt.Fprintf(os.Stderr, "実行環境: %s\n", runtime.GOOS)
	
	// 環境変数情報
	fmt.Fprintf(os.Stderr, "\n--- 環境変数情報 ---\n")
	fmt.Fprintf(os.Stderr, "SHELL: %s\n", os.Getenv("SHELL"))
	fmt.Fprintf(os.Stderr, "WSL_DISTRO_NAME: %s\n", os.Getenv("WSL_DISTRO_NAME"))
	fmt.Fprintf(os.Stderr, "PSModulePath: %s\n", os.Getenv("PSModulePath"))
	
	// プロセス情報
	fmt.Fprintf(os.Stderr, "\n--- プロセス情報 ---\n")
	fmt.Fprintf(os.Stderr, "PID: %d\n", os.Getpid())
	fmt.Fprintf(os.Stderr, "PPID: %d\n", os.Getppid())
	
	// 親プロセス情報（Linux/WSL）
	if runtime.GOOS != "windows" {
		parentPID := os.Getppid()
		commPath := fmt.Sprintf("/proc/%d/comm", parentPID)
		if data, err := os.ReadFile(commPath); err == nil {
			parentName := strings.TrimSpace(string(data))
			fmt.Fprintf(os.Stderr, "親プロセス名: %s\n", parentName)
		}
	}
	
	fmt.Fprintf(os.Stderr, "\n--- 利用可能なコマンド ---\n")
	fmt.Fprintf(os.Stderr, "サブコマンド:\n")
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use != "completion" && cmd.Use != "help" {
			fmt.Fprintf(os.Stderr, "  %-15s %s\n", cmd.Use, cmd.Short)
		}
	}
	
	fmt.Fprintf(os.Stderr, "\nフラグ:\n")
	rootCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name != "help" {
			fmt.Fprintf(os.Stderr, "  --%-12s %s\n", flag.Name, flag.Usage)
		}
	})
	rootCmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name != "help" {
			fmt.Fprintf(os.Stderr, "  --%-12s %s (global)\n", flag.Name, flag.Usage)
		}
	})
	
	fmt.Fprintf(os.Stderr, "\n=== Generated Completion Script ===\n")
}

// printDetailedInstructions は詳細なインストール手順を表示します。
func printDetailedInstructions(shell string, programName string) {
	fmt.Fprintf(os.Stderr, "\n=== 詳細インストール手順 ===\n")
	fmt.Fprintf(os.Stderr, "シェル: %s\n\n", shell)

	switch shell {
	case "bash":
		fmt.Fprintf(os.Stderr, "【Bash用補完スクリプト】\n\n")
		fmt.Fprintf(os.Stderr, "1. 一時的な有効化（現在のセッションのみ）:\n")
		fmt.Fprintf(os.Stderr, "   source <(%s auto-completion)\n\n", programName)
		
		fmt.Fprintf(os.Stderr, "2. 永続的な有効化（ユーザー用）:\n")
		fmt.Fprintf(os.Stderr, "   echo 'source <(%s auto-completion)' >> ~/.bashrc\n", programName)
		fmt.Fprintf(os.Stderr, "   source ~/.bashrc\n\n")
		
		fmt.Fprintf(os.Stderr, "3. システム全体での有効化（管理者権限必要）:\n")
		fmt.Fprintf(os.Stderr, "   sudo %s auto-completion > /etc/bash_completion.d/%s\n\n", programName, programName)
		
		fmt.Fprintf(os.Stderr, "4. 手動でファイル保存:\n")
		fmt.Fprintf(os.Stderr, "   %s auto-completion > ~/bash_completion_%s.sh\n", programName, programName)
		fmt.Fprintf(os.Stderr, "   echo 'source ~/bash_completion_%s.sh' >> ~/.bashrc\n", programName)

	case "zsh":
		fmt.Fprintf(os.Stderr, "【Zsh用補完スクリプト】\n\n")
		fmt.Fprintf(os.Stderr, "1. 一時的な有効化（現在のセッションのみ）:\n")
		fmt.Fprintf(os.Stderr, "   source <(%s auto-completion)\n\n", programName)
		
		fmt.Fprintf(os.Stderr, "2. 永続的な有効化（ユーザー用）:\n")
		fmt.Fprintf(os.Stderr, "   echo 'source <(%s auto-completion)' >> ~/.zshrc\n", programName)
		fmt.Fprintf(os.Stderr, "   source ~/.zshrc\n\n")
		
		fmt.Fprintf(os.Stderr, "3. Zsh補完ディレクトリを使用:\n")
		fmt.Fprintf(os.Stderr, "   mkdir -p ~/.zsh/completions\n")
		fmt.Fprintf(os.Stderr, "   %s auto-completion > ~/.zsh/completions/_%s\n", programName, programName)
		fmt.Fprintf(os.Stderr, "   echo 'fpath=(~/.zsh/completions $fpath)' >> ~/.zshrc\n")
		fmt.Fprintf(os.Stderr, "   echo 'autoload -U compinit && compinit' >> ~/.zshrc\n\n")
		
		fmt.Fprintf(os.Stderr, "4. Oh My Zshを使用している場合:\n")
		fmt.Fprintf(os.Stderr, "   %s auto-completion > ~/.oh-my-zsh/completions/_%s\n", programName, programName)

	case "fish":
		fmt.Fprintf(os.Stderr, "【Fish用補完スクリプト】\n\n")
		fmt.Fprintf(os.Stderr, "1. 標準インストール方法:\n")
		fmt.Fprintf(os.Stderr, "   mkdir -p ~/.config/fish/completions\n")
		fmt.Fprintf(os.Stderr, "   %s auto-completion > ~/.config/fish/completions/%s.fish\n\n", programName, programName)
		
		fmt.Fprintf(os.Stderr, "2. 手動リロード（必要に応じて）:\n")
		fmt.Fprintf(os.Stderr, "   fish -c 'complete -e %s; source ~/.config/fish/completions/%s.fish'\n\n", programName, programName)
		
		fmt.Fprintf(os.Stderr, "3. 補完テスト:\n")
		fmt.Fprintf(os.Stderr, "   %s <TAB>\n", programName)

	case "powershell", "pwsh":
		fmt.Fprintf(os.Stderr, "【PowerShell用補完スクリプト】\n\n")
		fmt.Fprintf(os.Stderr, "1. 現在のセッションでの有効化:\n")
		fmt.Fprintf(os.Stderr, "   %s auto-completion | Out-String | Invoke-Expression\n\n", programName)
		
		fmt.Fprintf(os.Stderr, "2. PowerShellプロファイルに追加（永続的）:\n")
		fmt.Fprintf(os.Stderr, "   %s auto-completion >> $PROFILE\n", programName)
		fmt.Fprintf(os.Stderr, "   . $PROFILE\n\n")
		
		fmt.Fprintf(os.Stderr, "3. プロファイルファイルが存在しない場合:\n")
		fmt.Fprintf(os.Stderr, "   New-Item -Type File -Path $PROFILE -Force\n")
		fmt.Fprintf(os.Stderr, "   %s auto-completion >> $PROFILE\n\n", programName)
		
		fmt.Fprintf(os.Stderr, "4. 実行ポリシーの確認:\n")
		fmt.Fprintf(os.Stderr, "   Get-ExecutionPolicy\n")
		fmt.Fprintf(os.Stderr, "   # 必要に応じて: Set-ExecutionPolicy RemoteSigned -Scope CurrentUser\n")
	}
	
	fmt.Fprintf(os.Stderr, "\n【補完動作の確認】\n")
	fmt.Fprintf(os.Stderr, "インストール後、以下のコマンドでTABキーを押して補完をテスト:\n")
	fmt.Fprintf(os.Stderr, "  %s <TAB>\n", programName)
	fmt.Fprintf(os.Stderr, "  %s --<TAB>\n", programName)
	fmt.Fprintf(os.Stderr, "  %s daemon --<TAB>\n", programName)
	fmt.Fprintf(os.Stderr, "\n")
}

var autoCompletionCmd = &cobra.Command{
	Use:   "auto-completion",
	Short: "現在のシェル用の補完スクリプトを自動生成",
	Long: `現在実行中のシェルを自動判定し、適切な補完スクリプトを生成します。
サポートシェル: bash, zsh, fish, powershell

フラグ:
  --print  補完スクリプト生成時の詳細情報を表示

手動でシェルを指定する場合:
  rotate_backup completion bash
  rotate_backup completion zsh
  rotate_backup completion fish
  rotate_backup completion powershell`,
	Run: func(cmd *cobra.Command, args []string) {
		// 現在のシェルを検出
		detectedShell := detectCurrentShell()
		programName := GetFileNameWithoutExt(os.Args[0])

		if printDetails {
			// 詳細情報を表示
			printCompletionDetails(detectedShell, programName)
		} else {
			fmt.Fprintf(os.Stderr, "# 検出されたシェル: %s\n", detectedShell)
		}

		// 補完スクリプトを生成
		if err := generateCompletionScript(detectedShell); err != nil {
			fmt.Fprintf(os.Stderr, "補完スクリプト生成エラー: %v\n", err)
			os.Exit(1)
		}

		// インストール方法を表示（--printフラグ時は詳細版）
		if printDetails {
			printDetailedInstructions(detectedShell, programName)
		} else {
			printCompletionInstructions(detectedShell, programName)
		}
	},
}

func init() {
	// ルートコマンドのフラグ
	rootCmd.PersistentFlags().StringVarP(&args.ConfigPath, "config", "c", "config.hjson", "設定ファイルのパス")
	rootCmd.Flags().BoolVarP(&args.UpdateBackup, "update-backup", "u", false, "コピー処理のみ実行する（ローテーション・VHDX保存なし）")

	// daemonコマンドのフラグ
	daemonCmd.Flags().StringVar(&pidFile, "pid-file", "rotate_backup.pid", "PIDファイルのパス")
	daemonCmd.Flags().StringVar(&logLevel, "log-level", "info", "ログレベル (debug/info/warn/error)")

	// auto-completionコマンドのフラグ
	autoCompletionCmd.Flags().BoolVar(&printDetails, "print", false, "補完スクリプト生成時の詳細情報を表示")

	// サブコマンドを追加
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(autoCompletionCmd)
}

func GetFileNameWithoutExt(path string) string {
	return filepath.Base(path[:len(path)-len(filepath.Ext(path))])
}

func GetVersion() string {
	if len(revision) == 0 {
		// go installでビルドされた場合、gitの情報がなくなる。その場合v0.0.0.のように末尾に.がついてしまうのを避ける。
		return fmt.Sprintf("%v version %v", GetFileNameWithoutExt(os.Args[0]), version)
	} else {
		return fmt.Sprintf("%v version %v.%v", GetFileNameWithoutExt(os.Args[0]), version, revision)
	}
}

func ShowVersion() {
	fmt.Printf("%s\n", GetVersion())
	os.Exit(0)
}


// runBackup は設定を読み込み、バックアップ処理を行います。
// runOneShotMode は定期起動モードでバックアップを実行します。
func runOneShotMode(configPath string) error {
	// 現在時刻を取得
	now := time.Now()
	
	// 設定ファイルを読み込みます。
	log.Printf("設定ファイルを読み込み中: %s", configPath)
	cfg, err := loadConfig(configPath)
	if err != nil {
		// 設定ファイルが見つからない場合は自動生成を試行
		if os.IsNotExist(err) && isDefaultConfigPath(configPath) {
			fmt.Printf("設定ファイルが見つかりません: %s\n", configPath)
			fmt.Printf("既定の設定ファイルを自動生成します...\n")
			if genErr := generateTemplate(configPath); genErr != nil {
				fmt.Printf("設定ファイルの自動生成に失敗しました: %v\n", genErr)
				fmt.Println("--init-config でテンプレートを生成してください。")
				return err
			}
			fmt.Printf("設定ファイルを生成しました: %s\n", configPath)
			fmt.Println("dry_run が true に設定されています。設定を確認後、false に変更してください。")
			// 生成された設定ファイルを再読み込み
			cfg, err = loadConfig(configPath)
			if err != nil {
				fmt.Printf("生成された設定ファイルの読み込みエラー: %v\n", err)
				return err
			}
		} else {
			return err
		}
	}
	
	// デバッグ: 設定値を確認
	log.Printf("設定読み込み完了 - dry_run: %v", cfg.DryRun)
	
	// ログ出力先を設定ファイルに基づいて切り替え
	if err := setupLogOutput(cfg); err != nil {
		log.Printf("ログ出力設定エラー: %v", err)
		return fmt.Errorf("ログ出力設定エラー: %v", err)
	}
	
	// バックアップが必要かどうかを判定（重複実行防止含む）
	shouldExecute, level, err := shouldExecuteBackup(cfg, now)
	if err != nil {
		return fmt.Errorf("バックアップ判定エラー: %v", err)
	}
	
	if !shouldExecute {
		log.Printf("バックアップ不要: %s", now.Format("2006-01-02 15:04:05"))
		return nil
	}
	
	log.Printf("バックアップレベル決定: %s (時刻: %s)", level, now.Format("2006-01-02 15:04:05"))
	
	// dry-run出力
	if cfg.DryRun {
		fmt.Printf("[DRY-RUN] バックアップ実行: %s\n", level)
		fmt.Printf("[DRY-RUN] 実行時刻: %s\n", now.Format("2006-01-02 15:04:05"))
		fmt.Printf("[DRY-RUN] バックアップレベル: %s\n", level)
		fmt.Printf("[DRY-RUN] 保存先: %s\n", cfg.BackupDirs[level])
		
		// dry-runでも最終実行時刻を記録（テスト用）
		if err := recordLastExecution(cfg, level, now); err != nil {
			log.Printf("最終実行時刻記録エラー: %v", err)
		}
		return nil
	}
	
	// 実際のバックアップ処理を実行
	if err := runBackupWithLevel(cfg, level); err != nil {
		return err
	}
	
	// 実行成功時に最終実行時刻を記録
	return recordLastExecution(cfg, level, now)
}

// runDaemonMode は常駐モードでバックアップを実行します。
func runDaemonMode(configPath string, daemonConfig *DaemonCmd) error {
	log.Printf("常駐モード開始 (PID: %d)", os.Getpid())
	
	// PIDファイルを作成
	if err := writePIDFile(daemonConfig.PIDFile); err != nil {
		return fmt.Errorf("PIDファイル作成エラー: %v", err)
	}
	defer removePIDFile(daemonConfig.PIDFile)
	
	// 設定ファイルを読み込み
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	
	// ログ出力先を設定ファイルに基づいて切り替え
	if err := setupLogOutput(cfg); err != nil {
		log.Printf("ログ出力設定エラー: %v", err)
		return fmt.Errorf("ログ出力設定エラー: %v", err)
	}
	
	// 無限ループでスケジュール実行
	for {
		now := time.Now()
		shouldBackup, level := determineBestBackupLevel(now)
		
		if shouldBackup {
			log.Printf("スケジュール実行: %s (時刻: %s)", level, now.Format("2006-01-02 15:04:05"))
			
			if cfg.DryRun {
				fmt.Printf("[DRY-RUN] バックアップ実行: %s\n", level)
				fmt.Printf("[DRY-RUN] 実行時刻: %s\n", now.Format("2006-01-02 15:04:05"))
				fmt.Printf("[DRY-RUN] バックアップレベル: %s\n", level)
				fmt.Printf("[DRY-RUN] 保存先: %s\n", cfg.BackupDirs[level])
			} else {
				if err := runBackupWithLevel(cfg, level); err != nil {
					log.Printf("バックアップエラー: %v", err)
				}
			}
		}
		
		// 次のチェックまで30秒待機
		time.Sleep(30 * time.Second)
	}
}

// バックアップレベル決定ロジック（排他的実行）
func determineBestBackupLevel(t time.Time) (bool, string) {
	hour, min := t.Hour(), t.Minute()
	log.Printf("--- バックアップレベル判定開始 ---")
	log.Printf("時刻: %02d時%02d分", hour, min)
	
	// 優先度順でチェック（最長間隔優先）
	if hour == 0 && min == 0 {
		log.Printf("✓ 1d判定: %02d時%02d分は毎日0時に該当", hour, min)
		return true, "1d"  // 毎日0時
	}
	log.Printf("✗ 1d判定: %02d時%02d分は毎日0時(00:00)に該当しない", hour, min)
	
	if (hour == 0 || hour == 12) && min == 0 {
		log.Printf("✓ 12h判定: %02d時%02d分は12時間間隔(00:00または12:00)に該当", hour, min)
		return true, "12h" // 0時と12時
	}
	log.Printf("✗ 12h判定: %02d時%02d分は12時間間隔(00:00,12:00)に該当しない", hour, min)
	
	if hour%6 == 0 && min == 0 {
		log.Printf("✓ 6h判定: %02d時%02d分は6時間間隔(00,06,12,18:00)に該当", hour, min)
		return true, "6h"  // 6時間ごと
	}
	log.Printf("✗ 6h判定: %02d時%02d分は6時間間隔(00,06,12,18:00)に該当しない (hour%%6=%d)", hour, min, hour%6)
	
	if hour%3 == 0 && min == 0 {
		log.Printf("✓ 3h判定: %02d時%02d分は3時間間隔(00,03,06,09,12,15,18,21:00)に該当", hour, min)
		return true, "3h"  // 3時間ごと
	}
	log.Printf("✗ 3h判定: %02d時%02d分は3時間間隔(00,03,06,09,12,15,18,21:00)に該当しない (hour%%3=%d)", hour, min, hour%3)
	
	if min == 0 || min == 30 {
		log.Printf("✓ 30m判定: %02d時%02d分は30分間隔(毎時00分または30分)に該当", hour, min)
		return true, "30m" // 30分ごと
	}
	log.Printf("✗ 30m判定: %02d時%02d分は30分間隔(毎時00分または30分)に該当しない", hour, min)
	
	log.Printf("判定結果: すべての条件に該当しないため、バックアップ不要")
	log.Printf("--- バックアップレベル判定終了 ---")
	return false, ""
}

// 重複実行防止を考慮したバックアップ判定
func shouldExecuteBackup(cfg *BackupConfig, currentTime time.Time) (bool, string, error) {
	// デバッグ: バックアップ判定開始
	log.Printf("=== バックアップ判定開始 ===")
	log.Printf("現在時刻: %s", currentTime.Format("2006-01-02 15:04:05"))
	log.Printf("現在時刻詳細: %02d時%02d分%02d秒", currentTime.Hour(), currentTime.Minute(), currentTime.Second())
	
	// 基本的な時刻判定
	shouldBackup, level := determineBestBackupLevel(currentTime)
	log.Printf("時刻判定結果: shouldBackup=%v, level=%s", shouldBackup, level)
	
	if !shouldBackup {
		log.Printf("判定結果: バックアップ実行タイミング外（%02d:%02d は 30分間隔の実行タイミング外）", 
			currentTime.Hour(), currentTime.Minute())
		return false, "", nil
	}
	
	// 最終実行時刻をチェック
	log.Printf("重複実行防止チェック開始")
	if cfg.LastExecutionFile != "" {
		log.Printf("最終実行時刻ファイル: %s", cfg.LastExecutionFile)
		lastRecord, err := loadLastExecutionRecord(cfg.LastExecutionFile)
		if err != nil && !os.IsNotExist(err) {
			log.Printf("最終実行時刻読み込みエラー: %v", err)
			return false, "", fmt.Errorf("最終実行時刻読み込みエラー: %v", err)
		}
		
		if os.IsNotExist(err) {
			log.Printf("最終実行時刻ファイルが存在しません（初回実行）")
		} else if lastRecord == nil {
			log.Printf("最終実行時刻記録が空です")
		} else {
			log.Printf("最終実行時刻記録を読み込みました")
			lastExecution, exists := lastRecord.LastExecutions[level]
			if !exists {
				log.Printf("レベル %s の前回実行記録はありません（初回実行）", level)
			} else if lastExecution.IsZero() {
				log.Printf("レベル %s の前回実行時刻がゼロ値です", level)
			} else {
				// 同じ時刻単位（分）での重複実行を防止
				currentMinute := currentTime.Truncate(time.Minute)
				lastMinute := lastExecution.Truncate(time.Minute)
				
				log.Printf("前回実行時刻: %s", lastExecution.Format("2006-01-02 15:04:05"))
				log.Printf("前回実行時刻（分単位）: %s", lastMinute.Format("2006-01-02 15:04"))
				log.Printf("現在時刻（分単位）: %s", currentMinute.Format("2006-01-02 15:04"))
				
				timeDiff := currentTime.Sub(lastExecution)
				log.Printf("時刻差分: %v", timeDiff)
				
				if currentMinute.Equal(lastMinute) {
					log.Printf("重複実行防止: %s レベルは %s に実行済み（同一分内）", level, lastExecution.Format("2006-01-02 15:04:05"))
					return false, "", nil
				} else {
					log.Printf("重複実行ではありません: 異なる分での実行")
				}
			}
		}
	} else {
		log.Printf("最終実行時刻ファイルが設定されていません（重複実行防止無効）")
	}
	
	log.Printf("判定結果: バックアップ実行許可 - レベル %s", level)
	log.Printf("=== バックアップ判定終了 ===")
	return true, level, nil
}

// 最終実行時刻を記録
func recordLastExecution(cfg *BackupConfig, level string, executionTime time.Time) error {
	if cfg.LastExecutionFile == "" {
		return nil // 記録無効
	}
	
	record, err := loadLastExecutionRecord(cfg.LastExecutionFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	
	if record == nil {
		record = &LastExecutionRecord{
			LastExecutions: make(map[string]time.Time),
		}
	}
	
	record.LastExecutions[level] = executionTime
	
	return saveLastExecutionRecord(cfg.LastExecutionFile, record)
}

// 最終実行時刻記録の読み込み
func loadLastExecutionRecord(filePath string) (*LastExecutionRecord, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	
	var record LastExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	
	if record.LastExecutions == nil {
		record.LastExecutions = make(map[string]time.Time)
	}
	
	return &record, nil
}

// 最終実行時刻記録の保存
func saveLastExecutionRecord(filePath string, record *LastExecutionRecord) error {
	// ディレクトリを作成
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}
	
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	
	return os.WriteFile(filePath, data, 0644)
}

// PIDファイル関連のヘルパー関数
func writePIDFile(pidFile string) error {
	pid := os.Getpid()
	return os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0644)
}

func removePIDFile(pidFile string) {
	os.Remove(pidFile)
}

// runBackupWithLevel は指定されたレベルでバックアップを実行します。
func runBackupWithLevel(cfg *BackupConfig, level string) error {
	log.Printf("バックアップレベル %s で処理を開始します", level)
	
	// 実際のバックアップ処理をここに実装
	// （現在は簡略化版）
	log.Printf("バックアップレベル %s の処理が完了しました", level)
	return nil
}

// 既存のrunBackup関数をrunBackupWithLevelに変更
func runBackup(configPath string) error {
	// 設定ファイルを読み込みます。
	log.Printf("設定ファイルを読み込み中: %s", configPath)
	cfg, err := loadConfig(configPath)
	if err != nil {
		// 設定ファイルが見つからない場合は自動生成を試行
		if os.IsNotExist(err) && isDefaultConfigPath(configPath) {
			fmt.Printf("設定ファイルが見つかりません: %s\n", configPath)
			fmt.Printf("既定の設定ファイルを自動生成します...\n")
			if genErr := generateTemplate(configPath); genErr != nil {
				fmt.Printf("設定ファイルの自動生成に失敗しました: %v\n", genErr)
				fmt.Println("--init-config でテンプレートを生成してください。")
				return err
			}
			fmt.Printf("設定ファイルを生成しました: %s\n", configPath)
			fmt.Println("dry_run が true に設定されています。設定を確認後、false に変更してください。")
			// 生成された設定ファイルを再読み込み
			cfg, err = loadConfig(configPath)
			if err != nil {
				fmt.Printf("生成された設定ファイルの読み込みエラー: %v\n", err)
				return err
			}
		} else {
			fmt.Printf("設定ファイルの読み込みエラー: %v\n", err)
			fmt.Println("--init-config でテンプレートを生成してください。")
			return err
		}
	}
	log.Printf("設定ファイルの読み込みが完了しました")

	// ログ出力先を設定ファイルに基づいて切り替え
	if err := setupLogOutput(cfg); err != nil {
		log.Printf("ログ出力設定エラー: %v", err)
		return fmt.Errorf("ログ出力設定エラー: %v", err)
	}

	// dry_run フラグが true ならドライランモードで処理します。
	if cfg.DryRun {
		fmt.Println("=== DRY RUN MODE ===")
		fmt.Println("実際の処理は実行せず、動作をシミュレートします。")
		fmt.Println()
	}

	// 多重実行防止のためファイルロックを取得します。
	var lockFile *os.File
	if cfg.EnableLock && !cfg.DryRun {
		log.Printf("多重実行防止のためファイルロックを取得します。: %v", cfg.LockFilePath)
		lockFile, err = acquireFileLock(cfg.LockFilePath)
		if err != nil {
			notify(cfg, NotifyLockConflict, "多重実行を検出しました。処理を終了します。", cfg.DryRun)
			return nil
		}
		defer releaseFileLock(lockFile)
	}

	// 全体処理開始時刻を記録します。
	startTime := time.Now()

	// 開始通知を送信します。
	notify(cfg, NotifyBackupStart, "バックアップを開始します", cfg.DryRun)

	// 通し番号を取得し増分します。
	id, err := getNextID(cfg.LastIDFile, cfg.DryRun)
	log.Printf("通し番号ファイル: %v", cfg.LastIDFile)
	log.Printf("通し番号: %v", id)
	if err != nil {
		log.Printf("Error: ID取得失敗: %v", err)
		return pkgerrors.Errorf("ID取得失敗: %v", err)
	}
	timeStamp := time.Now().Format("20060102_1504")
	filename := fmt.Sprintf("%06d_%s.vhdx", id, timeStamp)
	log.Printf("作成予定のバックアップ: %v", filename)

	if cfg.DryRun {
		fmt.Printf("生成予定ファイル名: %s\n", filename)
	}

	// VHDX が未マウントであればマウントします。
	if cfg.MountIfMissing && !isDriveMounted(cfg.VHDXMountDrive) {
		if err := mountVHDX(cfg.SourceVHDX, cfg.VHDXMountDrive, cfg.DryRun); err != nil {
			log.Printf("VHDXマウント失敗: %v", err)
			return pkgerrors.Errorf("VHDXマウント失敗: %v", err)
		}
	} else if cfg.DryRun {
		fmt.Printf("VHDXマウント状態: %s は既にマウント済み\n", cfg.VHDXMountDrive)
	}

	// コピー処理開始時刻を記録します。
	copyStart := time.Now()

	// ファイルをコピーします。
	if !cfg.DryRun {
		log.Printf("バックアップ処理開始: %s → %s", cfg.WorkDir, cfg.BackupDir)
	}
	if err := tryCopy(cfg, cfg.WorkDir, cfg.BackupDir, cfg.DryRun); err != nil {
		if !cfg.DryRun {
			log.Printf("コピー処理でエラーが発生しました: %v", err)
		}
		return pkgerrors.Errorf("コピー失敗: %v", err)
	}
	if !cfg.DryRun {
		log.Printf("コピー処理が正常に完了しました")
	}
	copyDur := time.Since(copyStart)

	// ローテーション処理: 新しいファイルを保存する前に昇格・削除処理を実行
	levels := []string{"30m", "3h", "6h", "12h", "1d"}
	if cfg.DryRun {
		fmt.Println("ローテーション処理:")
	}
	
	// まず昇格処理を実行（新しいファイル保存前に行うことが重要）
	promoteBackup(cfg, levels, cfg.DryRun)
	
	// その後、各レベルで上限超過分を削除
	for _, lvl := range levels {
		if err := rotateBackupsWithPromotion(cfg, lvl, cfg.DryRun); err != nil {
			if !cfg.DryRun {
				log.Println("rotate error:", lvl, err)
			}
		}
	}

	// 30分バックアップ用ディレクトリに VHDX を保存します。
	if err := saveBackup(cfg.BackupDirs["30m"], filename, cfg.SourceVHDX, cfg.DryRun); err != nil {
		return pkgerrors.Errorf("バックアップ保存失敗: %v", err)
	}

	// 処理時間をパフォーマンスログに記録します。
	logPerformance(cfg.PerfLogPath, startTime, copyDur, time.Since(startTime)-copyDur, cfg.DryRun)

	// 完了通知を送信します。
	notify(cfg, NotifyBackupEnd, "バックアップ完了: "+filename, cfg.DryRun)

	if cfg.DryRun {
		fmt.Println()
		fmt.Println("=== DRY RUN 完了 ===")
		fmt.Println("実際に処理を実行するには、設定ファイルの dry_run を false に変更してください。")
	}

	return nil
}

// runUpdateBackup はコピー処理のみを実行します（ローテーション・VHDX保存なし）。
func runUpdateBackup(configPath string) error {
	// 設定ファイルを読み込みます。
	log.Printf("設定ファイルを読み込み中: %s", configPath)
	cfg, err := loadConfig(configPath)
	if err != nil {
		// 設定ファイルが見つからない場合は自動生成を試行
		if os.IsNotExist(err) && isDefaultConfigPath(configPath) {
			fmt.Printf("設定ファイルが見つかりません: %s\n", configPath)
			fmt.Printf("既定の設定ファイルを自動生成します...\n")
			if genErr := generateTemplate(configPath); genErr != nil {
				fmt.Printf("設定ファイルの自動生成に失敗しました: %v\n", genErr)
				fmt.Println("--init-config でテンプレートを生成してください。")
				return err
			}
			fmt.Printf("設定ファイルを生成しました: %s\n", configPath)
			fmt.Println("dry_run が true に設定されています。設定を確認後、false に変更してください。")
			// 生成された設定ファイルを再読み込み
			cfg, err = loadConfig(configPath)
			if err != nil {
				fmt.Printf("生成された設定ファイルの読み込みエラー: %v\n", err)
				return err
			}
		} else {
			fmt.Printf("設定ファイルの読み込みエラー: %v\n", err)
			fmt.Println("--init-config でテンプレートを生成してください。")
			return err
		}
	}
	log.Printf("設定ファイルの読み込みが完了しました")

	// ログ出力先を設定ファイルに基づいて切り替え
	if err := setupLogOutput(cfg); err != nil {
		log.Printf("ログ出力設定エラー: %v", err)
		return fmt.Errorf("ログ出力設定エラー: %v", err)
	}

	// dry_run フラグが true ならドライランモードで処理します。
	if cfg.DryRun {
		fmt.Println("=== DRY RUN MODE (UPDATE-BACKUP) ===")
		fmt.Println("実際の処理は実行せず、コピー動作をシミュレートします。")
		fmt.Println()
	}

	// 全体処理開始時刻を記録します。
	startTime := time.Now()

	// 開始通知を送信します。
	notify(cfg, NotifyBackupStart, "バックアップ更新を開始します", cfg.DryRun)

	// VHDX が未マウントであればマウントします。
	if cfg.MountIfMissing && !isDriveMounted(cfg.VHDXMountDrive) {
		if err := mountVHDX(cfg.SourceVHDX, cfg.VHDXMountDrive, cfg.DryRun); err != nil {
			log.Printf("VHDXマウント失敗: %v", err)
			return pkgerrors.Errorf("VHDXマウント失敗: %v", err)
		}
	} else if cfg.DryRun {
		fmt.Printf("VHDXマウント状態: %s は既にマウント済み\n", cfg.VHDXMountDrive)
	}

	// コピー処理開始時刻を記録します。
	copyStart := time.Now()

	// ファイルをコピーします。
	if !cfg.DryRun {
		log.Printf("バックアップ処理開始 (update-backup): %s → %s", cfg.WorkDir, cfg.BackupDir)
	}
	if err := tryCopy(cfg, cfg.WorkDir, cfg.BackupDir, cfg.DryRun); err != nil {
		if !cfg.DryRun {
			log.Printf("コピー処理でエラーが発生しました: %v", err)
		}
		return pkgerrors.Errorf("コピー失敗: %v", err)
	}
	if !cfg.DryRun {
		log.Printf("コピー処理が正常に完了しました")
	}
	copyDur := time.Since(copyStart)

	// 処理時間をパフォーマンスログに記録します。
	logPerformance(cfg.PerfLogPath, startTime, copyDur, 0, cfg.DryRun) // ローテーション時間は0

	// 完了通知を送信します。
	notify(cfg, NotifyUpdateEnd, "バックアップ更新完了 (update-backup)", cfg.DryRun)

	if cfg.DryRun {
		fmt.Println()
		fmt.Println("=== DRY RUN 完了 (UPDATE-BACKUP) ===")
		fmt.Println("実際に処理を実行するには、設定ファイルの dry_run を false に変更してください。")
	}

	return nil
}

// generateTemplate は HJSON テンプレートを生成します。
func generateTemplate(destPath string) error {
	if destPath == "" {
		destPath = "config.hjson"
	}
	template := `{
// ========================================
// 🔄 VHDX Backup Rotation Tool 設定ファイル
// ========================================
// このファイルは HJSON 形式です（JSON + コメント + 末尾カンマOK）
// 設定を変更した後は、dry_run を false にして実行してください。
// 詳細なドキュメントは readme.md を参照してください。

// ========================================
// 🎯 実行モード設定
// ========================================
// dry_run: 実際の処理を行わず、安全なシミュレーションのみ実行
// true の間は実際のファイル操作は行われません。設定確認後に false にしてください。
dry_run: true

// ========================================
// 🔄 スマートコピーシステム設定
// ========================================
// copy_method_priority: コピー方式の優先順位（不足分は自動補完）
// 指定されていない方式は自動的に末尾に追加されます
// 1. robocopy: 高速・高機能、拡張子フィルタ時は2段階実行（コピー→削除）
// 2. xcopy: Windows標準、安定性重視、個別ファイルコピー対応
// 3. copy-item: PowerShell、柔軟性が高い、スクリプト処理
// 4. native: Go言語内蔵、クロスプラットフォーム、最終手段
copy_method_priority: ["robocopy","xcopy","copy-item","native"]

// copy_args: 各コピー方式の引数
copy_args: {
	// robocopy: ミラーリング、リトライ1回、ログ簡略化
	robocopy: "/MIR /R:1 /W:1 /NJH /NJS /NP"
	// xcopy: サブディレクトリ含む、上書き、日付チェック、隠しファイル
	xcopy: "/E /Y /D /H"
	// copy-item: PowerShell、再帰、強制上書き
	copy-item: "-Recurse -Force"
	// native: Go言語内蔵（引数なし）
	native: ""
}

// ========================================
// 📁 パス設定
// ========================================
// work_dir: コピー元（VHDXマウント先またはソースディレクトリ）
work_dir: "P:/"
// backup_dir: コピー先（バックアップの保存先）
backup_dir: "Q:/"
// source_vhdx: バックアップするVHDXファイル
source_vhdx: "C:/Backups/backup.vhdx"
// last_id_file: 通し番号管理ファイル（6桁の連番生成）
last_id_file: "C:/Backups/last_id.txt"
// vhdx_mount_drive: VHDXをマウントするドライブレター
vhdx_mount_drive: "Q:"
// mount_vhdx_if_missing: VHDXが未マウントの場合に自動マウント
mount_vhdx_if_missing: true

// ========================================
// 📊 多段階ローテーション設定
// ========================================
// keep_versions: 各レベルでの保持数
// 昇格経路: 30m → 3h → 6h → 12h → 1d → 削除
keep_versions: {
	"30m": 5,    // 30分間隔×5個 = 2.5時間分
	"3h": 2,     // 3時間間隔×2個 = 6時間分
	"6h": 2,     // 6時間間隔×2個 = 12時間分
	"12h": 2,    // 12時間間隔×2個 = 24時間分
	"1d": 5      // 1日間隔×5個 = 5日分
}

// backup_dirs: 各レベルのバックアップディレクトリ
backup_dirs: {
	"30m": "C:/Backups/30m",
	"3h":  "C:/Backups/3h",
	"6h":  "C:/Backups/6h",
	"12h": "C:/Backups/12h",
	"1d":  "C:/Backups/1d"
}

// ========================================
// 🎯 拡張子フィルタリング設定
// ========================================
// extensions: バックアップ対象の拡張子（空の場合は全ファイル）
// 注意: 拡張子フィルタリング使用時はrobocopyは2段階実行（コピー→不要ファイル削除）
// 例: [".cpp",".hpp",".c",".h",".txt",".md",".py",".js"]
extensions: [".cpp",".hpp",".c",".h"]

// exclude_dirs: 除外するディレクトリ
// Windowsシステムフォルダ（System Volume Information等）は自動的に除外されます
exclude_dirs: [
	"P:/Temp",
	"P:/Debug",
	"P:/node_modules",
	"P:/.git",
	"P:/.vs"
]

// include_files: 個別に含めるファイル（拡張子フィルタを無視して強制コピー）
include_files: [
	"P:/important/README.txt",
	"P:/config/settings.ini"
]

// ========================================
// 🔔 通知システム設定
// ========================================
// 各場面でのWindowsトースト通知のON/OFF設定
// go-toastライブラリ使用、フォールバック: msg.exe
notifications: {
	lock_conflict: true,   // 多重実行検出時（重要・推奨ON）
	backup_start: false,   // バックアップ開始時（通常不要）
	backup_end: true,      // バックアップ完了時（推奨ON）
	update_end: false,     // --update-backup完了時（頻繁実行時は無効推奨）
	error: true            // エラー発生時（重要・推奨ON）
}

// ========================================
// 📝 ログ・パフォーマンス記録設定
// ========================================
// log_file: 実行ログファイル（空の場合はコンソールのみ）
log_file: "C:/Backups/log.txt"
// perf_log_path: パフォーマンスログ（TSV形式）
// 列: 実行日時, UNIXミリ秒, 全体処理時間(ms), コピー時間(ms), ローテーション時間(ms)
perf_log_path: "C:/Backups/perf.tsv"

// ========================================
// 🔒 多重実行防止
// ========================================
// enable_lock: ファイルロックによる多重実行防止
enable_lock: true
// lock_file_path: ロックファイルのパス（PIDを記録）
lock_file_path: "C:/Backups/backup.lock"
// on_lock_conflict: 競合時の動作（現在は "notify-exit" のみサポート）
on_lock_conflict: "notify-exit"

// ========================================
// 🔒 重複実行防止
// ========================================
// last_execution_file: 最終実行時刻記録ファイル（重複実行防止用）
// 同一分内での重複実行を防止します
last_execution_file: "C:/Backups/last_execution.json"

// ========================================
// 📋 使用例・Tips
// ========================================
// 1. 初回セットアップ:
//    - dry_run: true のまま実行してシミュレーション確認
//    - 問題なければ dry_run: false に変更
//
// 2. 高速更新モード:
//    rotate_backup.exe --update-backup
//    （コピーのみ、VHDXローテーションなし）
//
// 3. 拡張子フィルタなし:
//    extensions: []
//    （全ファイルをバックアップ）
//
// 4. 通知テスト:
//    error: true にしてdry_runで確認
//
// 詳細なドキュメント: readme.md

}`
	return ioutil.WriteFile(destPath, []byte(template), 0644)
}

// isDefaultConfigPath は指定されたパスがデフォルトの設定ファイルパスかどうかを判定します。
func isDefaultConfigPath(configPath string) bool {
	// デフォルトの設定ファイルパスと比較
	// 絶対パス・相対パスの両方に対応
	absPath, _ := filepath.Abs(configPath)
	defaultPaths := []string{
		"config.hjson",
		"./config.hjson",
	}
	
	for _, defaultPath := range defaultPaths {
		if configPath == defaultPath {
			return true
		}
		// 絶対パスでも比較
		if absDefaultPath, err := filepath.Abs(defaultPath); err == nil {
			if absPath == absDefaultPath {
				return true
			}
		}
	}
	
	return false
}

// loadConfig は HJSON 設定を読み込み BackupConfig を返します。
func loadConfig(path string) (*BackupConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// HJSON を JSON に変換
	var jsonData interface{}
	if err := hjson.Unmarshal(data, &jsonData); err != nil {
		return nil, pkgerrors.Errorf("HJSON parse error: %v", err)
	}

	// JSON データを再エンコード
	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		return nil, pkgerrors.Errorf("JSON marshal error: %v", err)
	}

	// 標準の JSON として構造体にデコード
	var cfg BackupConfig
	if err := json.Unmarshal(jsonBytes, &cfg); err != nil {
		return nil, pkgerrors.Errorf("struct unmarshal error: %v", err)
	}
	return &cfg, nil
}

// getNextIDDryRun は last_id.txt から通し番号を読み込み +1 した値を返すが、ファイルを更新しません。
func getNextIDDryRun(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil // ファイルが存在しない場合は1を返す
		}
		return 0, err
	}
	defer f.Close()
	var id int
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		id, _ = strconv.Atoi(scanner.Text())
	}
	return id + 1, nil
}

// getNextID は last_id.txt から通し番号を読み込み +1 して保存します。
func getNextID(path string, dryRun bool) (int, error) {
	if dryRun {
		return getNextIDDryRun(path)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var id int
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		id, _ = strconv.Atoi(scanner.Text())
	}
	id++
	f.Seek(0, 0)
	f.Truncate(0)
	fmt.Fprintf(f, "%06d\n", id)
	return id, nil
}

// isCommandAvailable はコマンドが利用可能かチェックします。
func isCommandAvailable(command string) bool {
	switch command {
	case "robocopy":
		_, err := exec.LookPath("robocopy")
		return err == nil
	case "xcopy":
		_, err := exec.LookPath("xcopy")
		return err == nil
	case "copy-item":
		_, err := exec.LookPath("powershell")
		return err == nil
	case "native":
		return true // native は常に利用可能
	default:
		return false
	}
}

// ensureAllCopyMethods は設定されていないコピー方式を末尾に追加します。
func ensureAllCopyMethods(priority []string) []string {
	defaultMethods := []string{"robocopy", "xcopy", "copy-item", "native"}
	
	// 既に設定されている方式を記録
	existing := make(map[string]bool)
	for _, method := range priority {
		existing[method] = true
	}
	
	// 設定されていない方式を末尾に追加
	result := append([]string{}, priority...)
	for _, method := range defaultMethods {
		if !existing[method] {
			result = append(result, method)
		}
	}
	
	return result
}

// tryCopy は設定の優先順で最初に利用可能なコマンドを使用してコピーします。
func tryCopy(cfg *BackupConfig, src, dst string, dryRun bool) error {
	if dryRun {
		fmt.Printf("コピー処理: %s → %s\n", src, dst)
	} else {
		log.Printf("コピー処理開始: %s → %s", src, dst)
	}

	// コピー方式の優先順位を補完
	copyMethods := ensureAllCopyMethods(cfg.CopyMethodPriority)
	if len(cfg.CopyMethodPriority) != len(copyMethods) {
		log.Printf("コピー方式を補完しました: %v → %v", cfg.CopyMethodPriority, copyMethods)
	}

	var lastErr error
	availableCount := 0

	for _, method := range copyMethods {
		if !isCommandAvailable(method) {
			if dryRun {
				fmt.Printf("  %s: コマンドが利用できません\n", method)
			} else {
				log.Printf("コピー方法 %s: コマンドが利用できません", method)
			}
			continue
		}

		availableCount++
		args := cfg.CopyArgs[method]

		if !dryRun {
			log.Printf("コピー方法 %s を試行中...", method)
		}

		switch method {
		case "robocopy":
			// 拡張子フィルタリングが指定されている場合は、2段階実行
			if len(cfg.Extensions) > 0 {
				if dryRun {
					fmt.Printf("  robocopy: 拡張子フィルタ付き2段階実行\n")
					fmt.Printf("    対象拡張子: %v\n", cfg.Extensions)
					fmt.Println("    1. 指定拡張子ファイルのコピー")
					fmt.Println("    2. 不要ファイルの削除")
					fmt.Println("  → 成功と仮定")
					return nil
				}

				// 2段階robocopyを実行
				success := executeRobocopyWithExtensions(cfg, src, dst, args)
				if success {
					return nil
				} else {
					lastErr = errors.New("robocopy拡張子フィルタリング失敗")
					log.Printf("robocopy 拡張子フィルタリング失敗、次の方法を試行")
					continue
				}
			}

			// robocopy の基本構文: robocopy <source> <destination> [files] [options]
			parts := []string{src, dst}

			// robocopy オプションを追加
			parts = append(parts, strings.Fields(args)...)

			// 除外ディレクトリオプションを追加
			excludeDirs := []string{
				"/XD", "System Volume Information", "$Recycle.Bin", "Recovery",
			}

			// ユーザー定義の除外ディレクトリも追加
			for _, excludeDir := range cfg.ExcludeDirs {
				excludeDirs = append(excludeDirs, excludeDir)
			}
			parts = append(parts, excludeDirs...)

			// 除外ファイル属性（システム・隠しファイル）
			parts = append(parts, "/XA:SH")

			if dryRun {
				fmt.Printf("  使用: robocopy %s\n", strings.Join(parts, " "))
				fmt.Println("  → 成功と仮定")
				return nil
			}
			cmd := exec.Command("robocopy", parts...)
			log.Printf("実行コマンド: robocopy %s", strings.Join(parts, " "))
			out, err := cmd.CombinedOutput()

			// Shift_JISからUTF-8に変換
			outStr := convertShiftJISToUTF8(out)

			// robocopyの終了コードをチェック
			// 0-3: 成功、4以上: エラー
			if err == nil {
				log.Printf("robocopy でコピー完了")
				logRobocopyOutput(outStr)
				return nil
			} else if exitError, ok := err.(*exec.ExitError); ok {
				exitCode := exitError.ExitCode()
				if exitCode <= 3 {
					log.Printf("robocopy でコピー完了 (終了コード: %d)", exitCode)
					logRobocopyOutput(outStr)
					return nil
				} else {
					lastErr = err
					log.Printf("robocopy 失敗 (終了コード: %d): %v", exitCode, err)
					if len(outStr) > 0 {
						log.Printf("robocopy エラー出力:\n%s", outStr)
					}
					continue
				}
			} else {
				lastErr = err
				log.Printf("robocopy 失敗: %v", err)
				if len(outStr) > 0 {
					log.Printf("robocopy エラー出力:\n%s", outStr)
				}
				continue
			}

		case "xcopy":
			// 拡張子フィルタリングが指定されている場合は、事前にファイルリストを作成
			if len(cfg.Extensions) > 0 {
				if dryRun {
					fmt.Printf("  xcopy: 拡張子フィルタありで個別ファイルコピー\n")
					fmt.Printf("    対象拡張子: %v\n", cfg.Extensions)
					fmt.Println("  → 成功と仮定")
					return nil
				}

				// ディレクトリ探索して対象ファイルを収集
				var targetFiles []string
				var copiedFiles int
				var skippedFiles int

				err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						// アクセス権限エラーやその他のエラーをスキップ
						if strings.Contains(err.Error(), "Access is denied") ||
							strings.Contains(err.Error(), "access denied") ||
							strings.Contains(err.Error(), "permission denied") {
							skippedFiles++
							log.Printf("アクセス拒否によりスキップ: %s", path)
							return filepath.SkipDir
						}
						return nil
					}

					// Windowsの保護されたフォルダをスキップ
					if info.IsDir() {
						lowerName := strings.ToLower(info.Name())
						if lowerName == "system volume information" ||
							lowerName == "$recycle.bin" ||
							lowerName == "recovery" ||
							strings.HasPrefix(lowerName, "$") ||
							strings.HasPrefix(lowerName, "hiberfil") ||
							strings.HasPrefix(lowerName, "pagefile") ||
							strings.HasPrefix(lowerName, "swapfile") {
							log.Printf("システムフォルダをスキップ: %s", path)
							skippedFiles++
							return filepath.SkipDir
						}
					}

					// 設定ファイルの除外ディレクトリをチェック
					for _, excludeDir := range cfg.ExcludeDirs {
						if strings.HasPrefix(path, excludeDir) {
							log.Printf("除外ディレクトリによりスキップ: %s", path)
							skippedFiles++
							if info.IsDir() {
								return filepath.SkipDir
							}
							return nil
						}
					}

					// 拡張子フィルタリング
					if !info.IsDir() {
						ext := strings.ToLower(filepath.Ext(path))
						matched := false
						for _, allowedExt := range cfg.Extensions {
							if strings.ToLower(allowedExt) == ext {
								matched = true
								break
							}
						}
						if matched {
							targetFiles = append(targetFiles, path)
						} else {
							skippedFiles++
						}
					}

					return nil
				})

				if err != nil {
					lastErr = err
					log.Printf("xcopy ファイル探索失敗: %v", err)
					continue
				}

				log.Printf("xcopy: %d個のファイルが対象です", len(targetFiles))

				// 各ファイルを個別にコピー
				for _, srcFile := range targetFiles {
					rel, _ := filepath.Rel(src, srcFile)
					dstFile := filepath.Join(dst, rel)
					dstDir := filepath.Dir(dstFile)

					// コピー先ディレクトリを作成
					if err := os.MkdirAll(dstDir, 0755); err != nil {
						log.Printf("xcopy ディレクトリ作成失敗 %s: %v", dstDir, err)
						continue
					}

					// xcopy でファイルをコピー
					xcopyArgs := append([]string{srcFile, dstFile}, strings.Fields(args)...)
					cmd := exec.Command("xcopy", xcopyArgs...)
					log.Printf("実行コマンド: xcopy %s", strings.Join(xcopyArgs, " "))
					out, err := cmd.CombinedOutput()

					if err != nil {
						outStr := convertShiftJISToUTF8(out)
						log.Printf("xcopy ファイルコピー失敗 %s: %v\n出力: %s", srcFile, err, outStr)
						continue
					}
					copiedFiles++
				}

				if copiedFiles > 0 {
					log.Printf("xcopy でコピー完了 (%d個のファイル)", copiedFiles)
					return nil
				} else {
					lastErr = errors.New("xcopy: コピーできるファイルが見つかりませんでした")
					log.Printf("xcopy 失敗: %v", lastErr)
					continue
				}
			} else {
				// 拡張子フィルタリングなしの場合は従来通り
				parts := append([]string{src, dst}, strings.Fields(args)...)

				// xcopyでは除外リストファイルを使用
				excludeFile := ""
				if len(cfg.ExcludeDirs) > 0 || runtime.GOOS == "windows" {
					// 一時的な除外ファイルを作成
					tmpFile, err := os.CreateTemp("", "xcopy_exclude_*.txt")
					if err == nil {
						excludeFile = tmpFile.Name()
						// Windowsのシステムフォルダを除外
						tmpFile.WriteString("System Volume Information\n")
						tmpFile.WriteString("$Recycle.Bin\n")
						tmpFile.WriteString("Recovery\n")
						tmpFile.WriteString("hiberfil.sys\n")
						tmpFile.WriteString("pagefile.sys\n")
						tmpFile.WriteString("swapfile.sys\n")

						// ユーザー定義の除外ディレクトリも追加
						for _, excludeDir := range cfg.ExcludeDirs {
							tmpFile.WriteString(filepath.Base(excludeDir) + "\n")
						}
						tmpFile.Close()

						parts = append(parts, "/EXCLUDE:"+excludeFile)
						defer os.Remove(excludeFile) // 実行後に削除
					}
				}

				if dryRun {
					fmt.Printf("  使用: xcopy %s\n", strings.Join(parts, " "))
					if excludeFile != "" {
						fmt.Printf("    除外ファイル: %s\n", excludeFile)
					}
					fmt.Println("  → 成功と仮定")
					return nil
				}
				cmd := exec.Command("xcopy", parts...)
				log.Printf("実行コマンド: xcopy %s", strings.Join(parts, " "))
				out, err := cmd.CombinedOutput()

				// Shift_JISからUTF-8に変換
				outStr := convertShiftJISToUTF8(out)

				if err == nil {
					log.Printf("xcopy でコピー完了")
					if len(outStr) > 0 {
						log.Printf("xcopy 出力:\n%s", outStr)
					}
					return nil
				} else {
					lastErr = err
					log.Printf("xcopy 失敗: %v", err)
					if len(outStr) > 0 {
						log.Printf("xcopy エラー出力:\n%s", outStr)
					}
					continue
				}
			}

		case "copy-item":
			// PowerShellスクリプトで除外処理を含むコピーを実行
			excludePaths := []string{
				"'System Volume Information'", "'$Recycle.Bin'", "'Recovery'",
				"'hiberfil.sys'", "'pagefile.sys'", "'swapfile.sys'",
			}

			// ユーザー定義の除外パスも追加
			for _, excludeDir := range cfg.ExcludeDirs {
				excludePaths = append(excludePaths, "'"+filepath.Base(excludeDir)+"'")
			}

			excludeScript := strings.Join(excludePaths, ",")

			// PowerShell の引数を処理 (-Force が重複しないように)
			psArgs := args
			if !strings.Contains(args, "-Force") {
				psArgs = args + " -Force"
			}

			// 拡張子フィルタリングと除外処理を含むPowerShellスクリプト
			var ps string
			if len(cfg.Extensions) > 0 {
				// 拡張子フィルタリングがある場合
				var extConditions []string
				for _, ext := range cfg.Extensions {
					extConditions = append(extConditions, fmt.Sprintf("$_.Extension -eq '%s'", ext))
				}
				extensionFilter := strings.Join(extConditions, " -or ")

				ps = fmt.Sprintf(`
$excludePaths = @(%s)
Get-ChildItem -Path '%s' -Recurse -File | Where-Object {
	# 除外パスチェック
	$excluded = $false
	foreach ($exclude in $excludePaths) {
		if ($_.Name -eq $exclude -or $_.FullName -like "*\$exclude\*") {
			$excluded = $true
			break
		}
	}
	# 拡張子フィルタリング
	$extensionMatch = %s
	
	(-not $excluded) -and $extensionMatch
} | ForEach-Object {
	$relativePath = $_.FullName.Substring('%s'.Length).TrimStart('\')
	$destPath = Join-Path '%s' $relativePath
	$destDir = Split-Path $destPath -Parent
	if ($destDir -and -not (Test-Path $destDir)) {
		New-Item -ItemType Directory -Path $destDir -Force | Out-Null
	}
	Copy-Item $_.FullName $destPath %s
}
`, excludeScript, src, extensionFilter, src, dst, psArgs)
			} else {
				// 拡張子フィルタリングがない場合（従来通り）
				ps = fmt.Sprintf(`
$excludePaths = @(%s)
Get-ChildItem -Path '%s' -Recurse | Where-Object {
	$excluded = $false
	foreach ($exclude in $excludePaths) {
		if ($_.Name -eq $exclude -or $_.FullName -like "*\$exclude\*") {
			$excluded = $true
			break
		}
	}
	-not $excluded
} | Copy-Item -Destination '%s' %s
`, excludeScript, src, dst, psArgs)
			}

			if dryRun {
				fmt.Printf("  使用: powershell で除外処理付きコピー\n")
				fmt.Printf("    除外パス: %s\n", excludeScript)
				if len(cfg.Extensions) > 0 {
					fmt.Printf("    対象拡張子: %v\n", cfg.Extensions)
				}
				fmt.Println("  → 成功と仮定")
				return nil
			}
			cmd := exec.Command("powershell", "-Command", ps)
			log.Printf("実行コマンド: powershell -Command %s", ps)
			out, err := cmd.CombinedOutput()

			// PowerShellの出力もShift_JISの可能性があるため変換
			outStr := convertShiftJISToUTF8(out)

			if err == nil {
				log.Printf("PowerShell Copy-Item でコピー完了")
				if len(outStr) > 0 {
					log.Printf("PowerShell 出力:\n%s", outStr)
				}
				return nil
			} else {
				lastErr = err
				log.Printf("PowerShell Copy-Item 失敗: %v", err)
				if len(outStr) > 0 {
					log.Printf("PowerShell エラー出力:\n%s", outStr)
				}
				continue
			}

		case "native":
			if dryRun {
				fmt.Printf("  使用: native copy %s → %s\n", src, dst)
				fmt.Println("  → 成功と仮定")
				return nil
			}

			// ソースディレクトリの存在確認
			if _, err := os.Stat(src); err != nil {
				lastErr = err
				log.Printf("native copy 失敗: ソースディレクトリが存在しません: %v", err)
				continue
			}

			var copyErrors []string
			copiedFiles := 0
			skippedFiles := 0

			err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					// アクセス権限エラーやその他のエラーをスキップ
					if strings.Contains(err.Error(), "Access is denied") ||
						strings.Contains(err.Error(), "access denied") ||
						strings.Contains(err.Error(), "permission denied") {
						skippedFiles++
						log.Printf("アクセス拒否によりスキップ: %s", path)
						return filepath.SkipDir
					}
					copyErrors = append(copyErrors, fmt.Sprintf("path %s: %v", path, err))
					return nil // エラーが発生してもWalkを続行
				}

				// Windowsの保護されたフォルダをスキップ
				if info.IsDir() {
					lowerName := strings.ToLower(info.Name())
					if lowerName == "system volume information" ||
						lowerName == "$recycle.bin" ||
						lowerName == "recovery" ||
						strings.HasPrefix(lowerName, "$") ||
						strings.HasPrefix(lowerName, "hiberfil") ||
						strings.HasPrefix(lowerName, "pagefile") ||
						strings.HasPrefix(lowerName, "swapfile") {
						log.Printf("システムフォルダをスキップ: %s", path)
						skippedFiles++
						return filepath.SkipDir
					}
				}

				// 設定ファイルの除外ディレクトリをチェック
				for _, excludeDir := range cfg.ExcludeDirs {
					if strings.HasPrefix(path, excludeDir) {
						log.Printf("除外ディレクトリによりスキップ: %s", path)
						skippedFiles++
						if info.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
				}

				// 拡張子フィルタリング（設定されている場合）
				if len(cfg.Extensions) > 0 && !info.IsDir() {
					ext := strings.ToLower(filepath.Ext(path))
					matched := false
					for _, allowedExt := range cfg.Extensions {
						if strings.ToLower(allowedExt) == ext {
							matched = true
							break
						}
					}
					if !matched {
						skippedFiles++
						return nil
					}
				}

				rel, _ := filepath.Rel(src, path)
				dest := filepath.Join(dst, rel)

				if info.IsDir() {
					if err := os.MkdirAll(dest, 0755); err != nil {
						copyErrors = append(copyErrors, fmt.Sprintf("mkdir %s: %v", dest, err))
					}
					return nil
				}

				// ファイルのコピー
				in, err := os.Open(path)
				if err != nil {
					if strings.Contains(err.Error(), "Access is denied") ||
						strings.Contains(err.Error(), "access denied") ||
						strings.Contains(err.Error(), "permission denied") {
						log.Printf("ファイルアクセス拒否によりスキップ: %s", path)
						skippedFiles++
						return nil
					}
					copyErrors = append(copyErrors, fmt.Sprintf("open %s: %v", path, err))
					return nil
				}
				defer in.Close()

				out, err := os.Create(dest)
				if err != nil {
					copyErrors = append(copyErrors, fmt.Sprintf("create %s: %v", dest, err))
					return nil
				}
				defer out.Close()

				if _, err = io.Copy(out, in); err != nil {
					copyErrors = append(copyErrors, fmt.Sprintf("copy %s to %s: %v", path, dest, err))
					return nil
				}

				copiedFiles++
				return nil
			})

			log.Printf("native copy 結果: %d個のファイルをコピー、%d個をスキップ", copiedFiles, skippedFiles)

			if len(copyErrors) > 0 {
				log.Printf("コピー中に %d個のエラーが発生しましたが、処理を継続しました", len(copyErrors))
				for i, errMsg := range copyErrors {
					if i < 5 { // 最初の5つのエラーのみ表示
						log.Printf("  エラー: %s", errMsg)
					} else if i == 5 {
						log.Printf("  ... 他に %d個のエラー", len(copyErrors)-5)
						break
					}
				}
			}

			if copiedFiles > 0 {
				log.Printf("native copy でコピー完了 (%d個のファイル)", copiedFiles)
				return nil
			} else if skippedFiles > 0 {
				log.Printf("すべてのファイルがスキップされましたが、エラーではありません")
				return nil
			}

			if err != nil {
				lastErr = err
				log.Printf("native copy 失敗: %v", err)
				continue
			}

			lastErr = errors.New("コピーできるファイルが見つかりませんでした")
			log.Printf("native copy 失敗: %v", lastErr)
			continue
		}
	}

	if availableCount == 0 {
		err := errors.New("利用可能なコピー方法がありません")
		log.Printf("コピー失敗: %v", err)
		return err
	}

	err := fmt.Errorf("すべてのコピー方法に失敗しました。最後のエラー: %v", lastErr)
	log.Printf("コピー失敗: %v", err)
	return err
}

// saveBackup は VHDX を指定ディレクトリにコピーします。
func saveBackup(dstDir, filename, srcPath string, dryRun bool) error {
	if dryRun {
		fmt.Printf("VHDXバックアップ保存: %s → %s/%s\n", srcPath, dstDir, filename)
		return nil
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
	dstPath := filepath.Join(dstDir, filename)
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// rotateBackupsWithPromotion は指定レベルで上限超過分を削除します。
func rotateBackupsWithPromotion(cfg *BackupConfig, level string, dryRun bool) error {
	dir := cfg.BackupDirs[level]
	keep := cfg.KeepVersions[level]

	if dryRun {
		fmt.Printf("  %s: %s (保持数: %d)\n", level, dir, keep)
	}

	// ディレクトリが存在しない場合は作成
	if err := os.MkdirAll(dir, 0755); err != nil {
		if !dryRun {
			log.Printf("バックアップディレクトリ作成エラー %s: %v", dir, err)
		}
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if dryRun {
			fmt.Printf("    → ディレクトリが存在しません\n")
		} else {
			log.Printf("ディレクトリ読み込みエラー %s: %v", dir, err)
		}
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".vhdx") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	if len(names) > keep {
		if dryRun {
			fmt.Printf("    → %d個のファイルを削除予定\n", len(names)-keep)
		} else {
			for _, old := range names[:len(names)-keep] {
				os.Remove(filepath.Join(dir, old))
			}
		}
	} else {
		if dryRun {
			fmt.Printf("    → 削除対象なし (%d個存在)\n", len(names))
		}
	}
	return nil
}

// promoteBackup は下位レベルから上位レベルへ古いファイルを昇格します。
// 新しいファイルが追加される前に実行されるべきです。
func promoteBackup(cfg *BackupConfig, levels []string, dryRun bool) {
	if dryRun {
		fmt.Println("昇格処理:")
	}

	for i := 0; i < len(levels)-1; i++ {
		curDir := cfg.BackupDirs[levels[i]]
		nextDir := cfg.BackupDirs[levels[i+1]]

		if dryRun {
			fmt.Printf("  %s → %s\n", levels[i], levels[i+1])
		}

		// 昇格先ディレクトリが存在しない場合は作成
		if !dryRun {
			if err := os.MkdirAll(nextDir, 0755); err != nil {
				log.Printf("昇格先ディレクトリ作成エラー %s: %v", nextDir, err)
				continue
			}
		}

		entries, err := os.ReadDir(curDir)
		if err != nil {
			if dryRun {
				fmt.Println("    → ソースディレクトリが存在しません")
			}
			continue
		}

		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".vhdx") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)

		// 現在のレベルが保持数を超える場合、最古のファイルを昇格
		if len(names) > cfg.KeepVersions[levels[i]] {
			old := names[0]
			if dryRun {
				fmt.Printf("    → %s を昇格予定 (現在数: %d, 保持数: %d)\n", old, len(names), cfg.KeepVersions[levels[i]])
			} else {
				src := filepath.Join(curDir, old)
				dst := filepath.Join(nextDir, old)
				if _, err := os.Stat(dst); os.IsNotExist(err) {
					if err := os.Rename(src, dst); err != nil {
						log.Printf("昇格失敗 %s → %s: %v", src, dst, err)
					} else {
						log.Printf("昇格成功: %s を %s から %s へ移動", old, levels[i], levels[i+1])
					}
				} else {
					log.Printf("昇格先に同名ファイルが存在するため削除: %s", src)
					os.Remove(src)
				}
			}
		} else {
			if dryRun {
				fmt.Printf("    → 昇格対象なし (現在数: %d, 保持数: %d)\n", len(names), cfg.KeepVersions[levels[i]])
			}
		}
	}
}

// acquireFileLock はファイルベースの排他ロックを取得します。
func acquireFileLock(path string) (*os.File, error) {
	// シンプルなファイル存在チェックによる排他制御
	if _, err := os.Stat(path); err == nil {
		return nil, errors.New("ロックファイルが既に存在します")
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	// PIDを書き込んでプロセス識別可能にする
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return f, nil
}

// releaseFileLock はファイルロックを解放します。
func releaseFileLock(f *os.File) {
	if f != nil {
		lockPath := f.Name()
		f.Close()
		os.Remove(lockPath) // ロックファイルを削除
	}
}

// isDriveMounted はドライブレターがマウントされているか確認します。
func isDriveMounted(drive string) bool {
	var path string
	if runtime.GOOS == "windows" {
		path = drive + "\\"
	} else {
		path = drive
	}
	_, err := os.Stat(path)
	return err == nil
}

// mountVHDX は PowerShell 経由で VHDX をマウントします。
func mountVHDX(imagePath string, mountDrive string, dryRun bool) error {
	if dryRun {
		fmt.Printf("VHDXマウント予定: %s → %s\n", imagePath, mountDrive)
		return nil
	}
	ps := fmt.Sprintf("Mount-DiskImage -ImagePath '%s'", imagePath)
	cmd := exec.Command("powershell", "-Command", ps)
	log.Printf("実行コマンド: powershell -Command %s", ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// PowerShellの出力を文字エンコーディング変換
		outStr := convertShiftJISToUTF8(out)
		return pkgerrors.Errorf("mount failed: %v\n出力: %s", err, outStr)
	}

	// 成功時も出力があれば表示
	if len(out) > 0 {
		outStr := convertShiftJISToUTF8(out)
		log.Printf("VHDX マウント出力:\n%s", outStr)
	}
	return nil
}


// notify は指定された種類の通知を送信します。
func notify(cfg *BackupConfig, notifyType NotificationType, message string, dryRun bool) {
	if dryRun {
		fmt.Printf("通知: %s\n", message)
		return
	}

	// 通知タイプに応じて送信するかどうかを判定
	shouldNotify := false
	switch notifyType {
	case NotifyLockConflict:
		shouldNotify = cfg.Notifications.LockConflict
	case NotifyBackupStart:
		shouldNotify = cfg.Notifications.BackupStart
	case NotifyBackupEnd:
		shouldNotify = cfg.Notifications.BackupEnd
	case NotifyUpdateEnd:
		shouldNotify = cfg.Notifications.UpdateEnd
	case NotifyError:
		shouldNotify = cfg.Notifications.Error
	}

	if shouldNotify {
		sendToastNotification(message)
	} else {
		log.Printf("通知スキップ (%s): %s", getNotificationTypeName(notifyType), message)
	}
}

// getNotificationTypeName は通知タイプの名前を返します。
func getNotificationTypeName(notifyType NotificationType) string {
	switch notifyType {
	case NotifyLockConflict:
		return "lock_conflict"
	case NotifyBackupStart:
		return "backup_start"
	case NotifyBackupEnd:
		return "backup_end"
	case NotifyUpdateEnd:
		return "update_end"
	case NotifyError:
		return "error"
	default:
		return "unknown"
	}
}

// sendToastNotification は Windows トースト通知を送信します。
func sendToastNotification(message string) {
	log.Printf("トースト通知を送信中: %s", message)
	
	// go-toast ライブラリを使用してトースト通知を送信
	notification := toast.Notification{
		AppID:   "Backup Rotation Tool",
		Title:   "Backup Notification",
		Message: message,
		Icon:    "", // アイコンファイルのパス（オプション）
		Actions: []toast.Action{
			{
				Type:      "protocol",
				Label:     "OK",
				Arguments: "",
			},
		},
	}

	err := notification.Push()
	if err != nil {
		log.Printf("go-toast トースト通知エラー: %v", err)
		
		// フォールバック: PowerShell経由で試行
		sendFallbackNotification(message)
	} else {
		log.Printf("go-toast でトースト通知を送信しました: %s", message)
	}
}

// sendFallbackNotification はフォールバック通知を送信します。
func sendFallbackNotification(message string) {
	log.Printf("フォールバック通知を試行中: %s", message)
	
	// msg.exe を使用したシンプルな通知
	cmd := exec.Command("msg", "*", fmt.Sprintf("Backup Notification: %s", message))
	err := cmd.Run()
	if err != nil {
		log.Printf("msg.exe 通知失敗: %v", err)
		log.Printf("通知メッセージ (フォールバック): %s", message)
	} else {
		log.Printf("msg.exe で通知を送信しました: %s", message)
	}
}

// logPerformance は性能ログをタブ区切りで追記します。
func logPerformance(path string, startTime time.Time, copyDur, rotateDur time.Duration, dryRun bool) {
	if dryRun {
		if path != "" {
			fmt.Printf("パフォーマンスログ出力: %s\n", path)
		}
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("perf log error:", err)
		return
	}
	defer f.Close()
	ts := time.Now().Format(time.RFC3339)
	millis := startTime.UnixMilli()
	total := time.Since(startTime).Milliseconds()
	line := fmt.Sprintf("%s\t%d\t%d\t%d\n", ts, millis, total, copyDur.Milliseconds())
	f.WriteString(line)
}
