package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 拡張子フィルタリングのテスト用ヘルパー関数
func shouldFileBeIncluded(fileName string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	
	fileName = strings.ToLower(filepath.Base(fileName))
	
	for _, allowedExt := range extensions {
		allowedExt = strings.ToLower(allowedExt)
		if strings.HasSuffix(fileName, allowedExt) {
			return true
		}
	}
	
	return false
}

// 拡張子フィルタリングのテストケース
func TestExtensionFiltering(t *testing.T) {
	tests := []struct {
		name       string
		fileName   string
		extensions []string
		expected   bool
	}{
		// 基本的な拡張子テスト
		{
			name:       "基本的なC++ファイル",
			fileName:   "main.cpp",
			extensions: []string{".cpp", ".h"},
			expected:   true,
		},
		{
			name:       "基本的なヘッダーファイル",
			fileName:   "header.h",
			extensions: []string{".cpp", ".h"},
			expected:   true,
		},
		{
			name:       "対象外のファイル",
			fileName:   "readme.txt",
			extensions: []string{".cpp", ".h"},
			expected:   false,
		},
		// 複数拡張子のテスト
		{
			name:       "vcxprojファイル",
			fileName:   "project.vcxproj",
			extensions: []string{".cpp", ".h", ".vcxproj"},
			expected:   true,
		},
		{
			name:       "vcxproj.filtersファイル（設定有り）",
			fileName:   "project.vcxproj.filters",
			extensions: []string{".cpp", ".h", ".vcxproj.filters"},
			expected:   true,
		},
		{
			name:       "vcxproj.filtersファイル（設定無し）",
			fileName:   "project.vcxproj.filters",
			extensions: []string{".cpp", ".h", ".vcxproj"},
			expected:   false,
		},
		{
			name:       "tar.gzファイル",
			fileName:   "archive.tar.gz",
			extensions: []string{".tar.gz"},
			expected:   true,
		},
		{
			name:       "tar.gzファイル（.gzのみ設定）",
			fileName:   "archive.tar.gz",
			extensions: []string{".gz"},
			expected:   true,
		},
		// 大文字小文字のテスト
		{
			name:       "大文字拡張子ファイル",
			fileName:   "Main.CPP",
			extensions: []string{".cpp", ".h"},
			expected:   true,
		},
		{
			name:       "大文字設定の拡張子",
			fileName:   "main.cpp",
			extensions: []string{".CPP", ".H"},
			expected:   true,
		},
		// パス付きファイル名のテスト
		{
			name:       "パス付きファイル名",
			fileName:   "/path/to/file/main.cpp",
			extensions: []string{".cpp", ".h"},
			expected:   true,
		},
		{
			name:       "Windows形式パス",
			fileName:   "C:\\Project\\main.cpp",
			extensions: []string{".cpp", ".h"},
			expected:   true,
		},
		// 空の拡張子リストのテスト
		{
			name:       "拡張子フィルタ無し",
			fileName:   "anyfile.xyz",
			extensions: []string{},
			expected:   true,
		},
		// エッジケース
		{
			name:       "拡張子なしファイル",
			fileName:   "Makefile",
			extensions: []string{".cpp", ".h"},
			expected:   false,
		},
		{
			name:       "ドットで始まるファイル",
			fileName:   ".gitignore",
			extensions: []string{".gitignore"},
			expected:   true,
		},
		{
			name:       "ドットのみのファイル名",
			fileName:   ".",
			extensions: []string{".cpp"},
			expected:   false,
		},
		// 実際のVisual Studioファイルのテスト
		{
			name:       "solutionファイル",
			fileName:   "MySolution.sln",
			extensions: []string{".sln", ".vcxproj", ".vcxproj.filters"},
			expected:   true,
		},
		{
			name:       "vcxproj.userファイル",
			fileName:   "MyProject.vcxproj.user",
			extensions: []string{".sln", ".vcxproj", ".vcxproj.user"},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldFileBeIncluded(tt.fileName, tt.extensions)
			if result != tt.expected {
				t.Errorf("shouldFileBeIncluded(%q, %v) = %v, expected %v",
					tt.fileName, tt.extensions, result, tt.expected)
			}
		})
	}
}

// ベンチマークテスト
func BenchmarkExtensionFiltering(b *testing.B) {
	extensions := []string{".cpp", ".hpp", ".c", ".h", ".vcxproj", ".vcxproj.filters", ".sln"}
	testFiles := []string{
		"main.cpp",
		"header.h",
		"project.vcxproj",
		"project.vcxproj.filters",
		"solution.sln",
		"readme.txt",
		"data.xml",
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, fileName := range testFiles {
			shouldFileBeIncluded(fileName, extensions)
		}
	}
}

// 実際のファイルシステムでのテスト
func TestExtensionFilteringWithRealFiles(t *testing.T) {
	// テスト用の一時ディレクトリを作成
	tempDir, err := os.MkdirTemp("", "extension_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	// テスト用ファイルを作成
	testFiles := []string{
		"main.cpp",
		"header.h",
		"project.vcxproj",
		"project.vcxproj.filters",
		"readme.txt",
		"data.xml",
	}
	
	for _, fileName := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		file, err := os.Create(filePath)
		if err != nil {
			t.Fatalf("テストファイルの作成に失敗: %v", err)
		}
		file.Close()
	}
	
	// 拡張子フィルタのテスト
	extensions := []string{".cpp", ".h", ".vcxproj.filters"}
	
	expectedIncluded := []string{
		"main.cpp",
		"header.h",
		"project.vcxproj.filters",
	}
	
	expectedExcluded := []string{
		"project.vcxproj",
		"readme.txt",
		"data.xml",
	}
	
	// ファイルが存在することを確認
	for _, fileName := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Fatalf("テストファイルが存在しません: %s", filePath)
		}
	}
	
	// 含まれるべきファイルのテスト
	for _, fileName := range expectedIncluded {
		filePath := filepath.Join(tempDir, fileName)
		if !shouldFileBeIncluded(filePath, extensions) {
			t.Errorf("ファイル %s は含まれるべきですが除外されました", fileName)
		}
	}
	
	// 除外されるべきファイルのテスト
	for _, fileName := range expectedExcluded {
		filePath := filepath.Join(tempDir, fileName)
		if shouldFileBeIncluded(filePath, extensions) {
			t.Errorf("ファイル %s は除外されるべきですが含まれました", fileName)
		}
	}
}

// =============================================================================
// t-wada手法によるdry-runテスト
// =============================================================================

// テスト用時刻モック
type mockTime struct {
	current time.Time
}

func (mt *mockTime) Now() time.Time {
	return mt.current
}

func (mt *mockTime) SetTime(t time.Time) {
	mt.current = t
}

// テスト用設定生成
func createTestConfig(dryRun bool, workDir, backupDir string) *BackupConfig {
	return &BackupConfig{
		DryRun:    dryRun,
		WorkDir:   workDir,
		BackupDir: backupDir,
		KeepVersions: map[string]int{
			"30m": 5,
			"3h":  2,
			"6h":  2,
			"12h": 2,
			"1d":  5,
		},
		BackupDirs: map[string]string{
			"30m": filepath.Join(backupDir, "30m"),
			"3h":  filepath.Join(backupDir, "3h"),
			"6h":  filepath.Join(backupDir, "6h"),
			"12h": filepath.Join(backupDir, "12h"),
			"1d":  filepath.Join(backupDir, "1d"),
		},
		Extensions: []string{".cpp", ".h"},
		CopyMethodPriority: []string{"native"},
		LastIDFile: filepath.Join(backupDir, "last_id.txt"),
		LastExecutionFile: filepath.Join(backupDir, "last_execution.json"),
	}
}

// dry-run出力をキャプチャしてパースする関数
func captureBackupOutput(config *BackupConfig, mockTime time.Time) (string, error) {
	// 標準出力をキャプチャ
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	
	// バッファを準備
	var buf bytes.Buffer
	done := make(chan bool)
	
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			buf.WriteString(scanner.Text() + "\n")
		}
		done <- true
	}()
	
	// モックタイムを使ってバックアップ実行をシミュレート
	// 実際のバックアップ判定ロジックを呼び出し
	shouldBackup, level := determineBestBackupLevel(mockTime)
	
	if shouldBackup {
		fmt.Printf("[DRY-RUN] バックアップ実行: %s\n", level)
		fmt.Printf("[DRY-RUN] 実行時刻: %s\n", mockTime.Format("2006-01-02 15:04:05"))
		fmt.Printf("[DRY-RUN] バックアップレベル: %s\n", level)
		fmt.Printf("[DRY-RUN] 保存先: %s\n", config.BackupDirs[level])
	} else {
		fmt.Printf("[DRY-RUN] バックアップ不要: %s\n", mockTime.Format("2006-01-02 15:04:05"))
	}
	
	// 標準出力を復元
	w.Close()
	os.Stdout = oldStdout
	
	<-done
	return buf.String(), nil
}

// テスト側のdetermineBestBackupLevel関数は削除（main.goの関数を使用）

// =============================================================================
// 定期起動モードのテスト（t-wada手法）
// =============================================================================

func TestOneShotMode(t *testing.T) {
	// テスト用ディレクトリ作成
	tempDir, err := os.MkdirTemp("", "backup_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	workDir := filepath.Join(tempDir, "source")
	backupDir := filepath.Join(tempDir, "backup")
	os.MkdirAll(workDir, 0755)
	os.MkdirAll(backupDir, 0755)
	
	config := createTestConfig(true, workDir, backupDir)
	
	tests := []struct {
		name         string
		time         time.Time
		expectBackup bool
		expectLevel  string
	}{
		{
			name:         "00:00時は1dバックアップ",
			time:         time.Date(2025, 7, 1, 0, 0, 0, 0, time.Local),
			expectBackup: true,
			expectLevel:  "1d",
		},
		{
			name:         "12:00時は12hバックアップ",
			time:         time.Date(2025, 7, 1, 12, 0, 0, 0, time.Local),
			expectBackup: true,
			expectLevel:  "12h",
		},
		{
			name:         "06:00時は6hバックアップ",
			time:         time.Date(2025, 7, 1, 6, 0, 0, 0, time.Local),
			expectBackup: true,
			expectLevel:  "6h",
		},
		{
			name:         "09:00時は3hバックアップ",
			time:         time.Date(2025, 7, 1, 9, 0, 0, 0, time.Local),
			expectBackup: true,
			expectLevel:  "3h",
		},
		{
			name:         "09:30時は30mバックアップ",
			time:         time.Date(2025, 7, 1, 9, 30, 0, 0, time.Local),
			expectBackup: true,
			expectLevel:  "30m",
		},
		{
			name:         "09:15時はバックアップなし",
			time:         time.Date(2025, 7, 1, 9, 15, 0, 0, time.Local),
			expectBackup: false,
			expectLevel:  "",
		},
		// 排他的バックアップの確認
		{
			name:         "00:00時は12h/6h/3h/30mではない",
			time:         time.Date(2025, 7, 1, 0, 0, 0, 0, time.Local),
			expectBackup: true,
			expectLevel:  "1d", // 12hではない
		},
		{
			name:         "18:00時は6hバックアップ（12hではない）",
			time:         time.Date(2025, 7, 1, 18, 0, 0, 0, time.Local),
			expectBackup: true,
			expectLevel:  "6h", // 12hの時刻ではない
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := captureBackupOutput(config, tt.time)
			if err != nil {
				t.Fatalf("出力キャプチャに失敗: %v", err)
			}
			
			// Golden Testパターン：期待する出力内容をチェック
			if tt.expectBackup {
				expectedPatterns := []string{
					"\\[DRY-RUN\\] バックアップ実行: " + tt.expectLevel,
					"\\[DRY-RUN\\] 実行時刻: " + tt.time.Format("2006-01-02 15:04:05"),
					"\\[DRY-RUN\\] バックアップレベル: " + tt.expectLevel,
				}
				
				for _, pattern := range expectedPatterns {
					matched, err := regexp.MatchString(pattern, output)
					if err != nil {
						t.Fatalf("正規表現エラー: %v", err)
					}
					if !matched {
						t.Errorf("期待するパターンが見つかりません:\nパターン: %s\n出力:\n%s", pattern, output)
					}
				}
			} else {
				// バックアップ不要の場合
				expectedPattern := "\\[DRY-RUN\\] バックアップ不要"
				matched, err := regexp.MatchString(expectedPattern, output)
				if err != nil {
					t.Fatalf("正規表現エラー: %v", err)
				}
				if !matched {
					t.Errorf("バックアップ不要の出力が期待されます:\n出力:\n%s", output)
				}
			}
		})
	}
}

// =============================================================================
// 常駐モードのテスト（t-wada手法）
// =============================================================================

func TestDaemonMode24Hours(t *testing.T) {
	// テスト用ディレクトリ作成
	tempDir, err := os.MkdirTemp("", "daemon_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	workDir := filepath.Join(tempDir, "source")
	backupDir := filepath.Join(tempDir, "backup")
	os.MkdirAll(workDir, 0755)
	os.MkdirAll(backupDir, 0755)
	
	config := createTestConfig(true, workDir, backupDir)
	
	// 24時間のバックアップスケジュールをシミュレート
	_ = time.Date(2025, 7, 1, 9, 5, 0, 0, time.Local) // 開始時刻（参考用）
	
	// 期待される実行パターン
	expectedSchedule := []struct {
		time  time.Time
		level string
	}{
		{time.Date(2025, 7, 1, 9, 30, 0, 0, time.Local), "30m"},
		{time.Date(2025, 7, 1, 10, 0, 0, 0, time.Local), "30m"},
		{time.Date(2025, 7, 1, 10, 30, 0, 0, time.Local), "30m"},
		{time.Date(2025, 7, 1, 11, 0, 0, 0, time.Local), "30m"},
		{time.Date(2025, 7, 1, 11, 30, 0, 0, time.Local), "30m"},
		{time.Date(2025, 7, 1, 12, 0, 0, 0, time.Local), "12h"}, // 12hが優先
		{time.Date(2025, 7, 1, 12, 30, 0, 0, time.Local), "30m"},
		{time.Date(2025, 7, 1, 13, 0, 0, 0, time.Local), "30m"},
		{time.Date(2025, 7, 1, 15, 0, 0, 0, time.Local), "3h"},
		{time.Date(2025, 7, 1, 18, 0, 0, 0, time.Local), "6h"},  // 6hが優先
		{time.Date(2025, 7, 1, 21, 0, 0, 0, time.Local), "3h"},
		{time.Date(2025, 7, 2, 0, 0, 0, 0, time.Local), "1d"},   // 1dが最優先
	}
	
	var actualResults []struct {
		time  time.Time
		level string
	}
	
	// 24時間分のスケジュールをテスト
	for _, expected := range expectedSchedule {
		output, err := captureBackupOutput(config, expected.time)
		if err != nil {
			t.Fatalf("出力キャプチャに失敗: %v", err)
		}
		
		// バックアップが実行されることを確認
		backupExecuted := regexp.MustCompile(`\[DRY-RUN\] バックアップ実行: (\w+)`)
		matches := backupExecuted.FindStringSubmatch(output)
		
		if len(matches) > 1 {
			actualResults = append(actualResults, struct {
				time  time.Time
				level string
			}{
				time:  expected.time,
				level: matches[1],
			})
		}
	}
	
	// Golden Test: 期待されるスケジュールと実際の結果を比較
	if len(actualResults) != len(expectedSchedule) {
		t.Errorf("実行回数が異なります: 期待=%d, 実際=%d", len(expectedSchedule), len(actualResults))
	}
	
	for i, expected := range expectedSchedule {
		if i >= len(actualResults) {
			t.Errorf("実行結果が不足しています: %v %s", expected.time, expected.level)
			continue
		}
		
		actual := actualResults[i]
		if !actual.time.Equal(expected.time) || actual.level != expected.level {
			t.Errorf("スケジュール不一致:\n期待: %v %s\n実際: %v %s",
				expected.time, expected.level,
				actual.time, actual.level)
		}
	}
	
	// 各レベルの実行回数をカウント
	levelCounts := make(map[string]int)
	for _, result := range actualResults {
		levelCounts[result.level]++
	}
	
	// 期待される実行回数
	expectedCounts := map[string]int{
		"30m": 7, // 09:30, 10:00, 10:30, 11:00, 11:30, 12:30, 13:00
		"3h":  2, // 15:00, 21:00
		"6h":  1, // 18:00
		"12h": 1, // 12:00
		"1d":  1, // 00:00
	}
	
	for level, expectedCount := range expectedCounts {
		if actualCount := levelCounts[level]; actualCount != expectedCount {
			t.Errorf("レベル %s の実行回数が異なります: 期待=%d, 実際=%d", level, expectedCount, actualCount)
		}
	}
}

// =============================================================================
// 排他的バックアップロジックのテスト
// =============================================================================

func TestExclusiveBackupLogic(t *testing.T) {
	tests := []struct {
		name        string
		time        time.Time
		expectLevel string
		description string
	}{
		{
			name:        "00:00は1dが最優先",
			time:        time.Date(2025, 7, 1, 0, 0, 0, 0, time.Local),
			expectLevel: "1d",
			description: "12h/6h/3h/30mと重複するが1dが優先",
		},
		{
			name:        "12:00は12hが優先",
			time:        time.Date(2025, 7, 1, 12, 0, 0, 0, time.Local),
			expectLevel: "12h",
			description: "6h/3h/30mと重複するが12hが優先",
		},
		{
			name:        "06:00は6hが優先",
			time:        time.Date(2025, 7, 1, 6, 0, 0, 0, time.Local),
			expectLevel: "6h",
			description: "3h/30mと重複するが6hが優先",
		},
		{
			name:        "03:00は3hが優先",
			time:        time.Date(2025, 7, 1, 3, 0, 0, 0, time.Local),
			expectLevel: "3h",
			description: "30mと重複するが3hが優先",
		},
		{
			name:        "10:30は30mのみ",
			time:        time.Date(2025, 7, 1, 10, 30, 0, 0, time.Local),
			expectLevel: "30m",
			description: "他の間隔と重複しない",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldBackup, level := determineBestBackupLevel(tt.time)
			
			if !shouldBackup {
				t.Errorf("バックアップが実行されるべきです: %s", tt.description)
				return
			}
			
			if level != tt.expectLevel {
				t.Errorf("期待されるレベル: %s, 実際: %s (%s)", tt.expectLevel, level, tt.description)
			}
		})
	}
}

// =============================================================================
// 重複実行防止のテスト
// =============================================================================

func TestDuplicateExecutionPrevention(t *testing.T) {
	// テスト用ディレクトリ作成
	tempDir, err := os.MkdirTemp("", "duplicate_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	workDir := filepath.Join(tempDir, "source")
	backupDir := filepath.Join(tempDir, "backup")
	os.MkdirAll(workDir, 0755)
	os.MkdirAll(backupDir, 0755)
	
	config := createTestConfig(true, workDir, backupDir)
	
	// 9:30:00, 9:30:15, 9:30:45の同一分内での重複実行テスト
	testTimes := []time.Time{
		time.Date(2025, 7, 1, 9, 30, 0, 0, time.Local),   // 初回実行
		time.Date(2025, 7, 1, 9, 30, 15, 0, time.Local),  // 同一分内（実行されない）
		time.Date(2025, 7, 1, 9, 30, 45, 0, time.Local),  // 同一分内（実行されない）
		time.Date(2025, 7, 1, 10, 0, 0, 0, time.Local),   // 次の30分（実行される）
	}
	
	expectedResults := []bool{true, false, false, true}
	
	for i, testTime := range testTimes {
		t.Run(fmt.Sprintf("Test_%d_%s", i+1, testTime.Format("15:04:05")), func(t *testing.T) {
			shouldExecute, level, err := shouldExecuteBackup(config, testTime)
			if err != nil {
				t.Fatalf("shouldExecuteBackup エラー: %v", err)
			}
			
			expected := expectedResults[i]
			if shouldExecute != expected {
				t.Errorf("時刻 %s: 期待=%v, 実際=%v", testTime.Format("15:04:05"), expected, shouldExecute)
			}
			
			// 実行された場合は記録する
			if shouldExecute {
				if level != "30m" {
					t.Errorf("期待されるレベル: 30m, 実際: %s", level)
				}
				
				// 最終実行時刻を記録
				if err := recordLastExecution(config, level, testTime); err != nil {
					t.Fatalf("最終実行時刻記録エラー: %v", err)
				}
				
				// デバッグ情報
				t.Logf("時刻 %s で %s レベルを記録", testTime.Format("15:04:05"), level)
			} else {
				// デバッグ情報
				t.Logf("時刻 %s は実行されませんでした", testTime.Format("15:04:05"))
			}
		})
	}
}

func TestLastExecutionRecordPersistence(t *testing.T) {
	// テスト用ディレクトリ作成
	tempDir, err := os.MkdirTemp("", "persistence_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	recordFile := filepath.Join(tempDir, "last_execution.json")
	
	// テストデータを作成
	testTime := time.Date(2025, 7, 1, 9, 30, 0, 0, time.Local)
	originalRecord := &LastExecutionRecord{
		LastExecutions: map[string]time.Time{
			"30m": testTime,
			"3h":  testTime.Add(-3 * time.Hour),
		},
	}
	
	// 保存
	if err := saveLastExecutionRecord(recordFile, originalRecord); err != nil {
		t.Fatalf("レコード保存エラー: %v", err)
	}
	
	// 読み込み
	loadedRecord, err := loadLastExecutionRecord(recordFile)
	if err != nil {
		t.Fatalf("レコード読み込みエラー: %v", err)
	}
	
	// 検証
	if !loadedRecord.LastExecutions["30m"].Equal(testTime) {
		t.Errorf("30mレベルの時刻が一致しません: 期待=%v, 実際=%v", 
			testTime, loadedRecord.LastExecutions["30m"])
	}
	
	expectedThreeHour := testTime.Add(-3 * time.Hour)
	if !loadedRecord.LastExecutions["3h"].Equal(expectedThreeHour) {
		t.Errorf("3hレベルの時刻が一致しません: 期待=%v, 実際=%v", 
			expectedThreeHour, loadedRecord.LastExecutions["3h"])
	}
}

func TestMultipleLevelDuplicatePrevention(t *testing.T) {
	// 複数レベルでの重複実行防止テスト
	tempDir, err := os.MkdirTemp("", "multilevel_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	workDir := filepath.Join(tempDir, "source")
	backupDir := filepath.Join(tempDir, "backup")
	os.MkdirAll(workDir, 0755)
	os.MkdirAll(backupDir, 0755)
	
	config := createTestConfig(true, workDir, backupDir)
	
	// 00:00:00は1dレベル（最優先）
	testTime := time.Date(2025, 7, 1, 0, 0, 0, 0, time.Local)
	
	// 初回実行
	shouldExecute, level, err := shouldExecuteBackup(config, testTime)
	if err != nil {
		t.Fatalf("shouldExecuteBackup エラー: %v", err)
	}
	
	if !shouldExecute || level != "1d" {
		t.Errorf("初回実行失敗: shouldExecute=%v, level=%s", shouldExecute, level)
	}
	
	// 実行を記録
	if err := recordLastExecution(config, level, testTime); err != nil {
		t.Fatalf("実行記録エラー: %v", err)
	}
	
	// 同一分内での重複実行テスト
	duplicateTime := time.Date(2025, 7, 1, 0, 0, 30, 0, time.Local)
	shouldExecute2, _, err := shouldExecuteBackup(config, duplicateTime)
	if err != nil {
		t.Fatalf("重複チェックエラー: %v", err)
	}
	
	if shouldExecute2 {
		t.Error("重複実行が防止されていません")
	}
}

func TestConfigurationIssues(t *testing.T) {
	// 設定ファイルの問題をテスト
	tempDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	// LastExecutionFileが空の場合のテスト
	config := &BackupConfig{
		DryRun:            true,
		LastExecutionFile: "", // 空に設定
	}
	
	testTime := time.Date(2025, 7, 1, 9, 30, 0, 0, time.Local)
	
	// LastExecutionFileが空でも動作することを確認
	shouldExecute, level, err := shouldExecuteBackup(config, testTime)
	if err != nil {
		t.Fatalf("設定無しでのエラー: %v", err)
	}
	
	if !shouldExecute || level != "30m" {
		t.Errorf("設定無しでの実行失敗: shouldExecute=%v, level=%s", shouldExecute, level)
	}
	
	// 記録もエラーなく動作することを確認
	if err := recordLastExecution(config, level, testTime); err != nil {
		t.Errorf("設定無しでの記録エラー: %v", err)
	}
}

// =============================================================================
// シェル自動判定機能のテスト
// =============================================================================

func TestDetectCurrentShell(t *testing.T) {
	// 元の環境変数を保存
	originalShell := os.Getenv("SHELL")
	originalWSL := os.Getenv("WSL_DISTRO_NAME")
	originalPSModulePath := os.Getenv("PSModulePath")
	
	defer func() {
		// 環境変数を復元
		os.Setenv("SHELL", originalShell)
		os.Setenv("WSL_DISTRO_NAME", originalWSL)
		os.Setenv("PSModulePath", originalPSModulePath)
	}()
	
	tests := []struct {
		name            string
		shell           string
		wslDistro       string
		psModulePath    string
		expectedShell   string
		description     string
	}{
		{
			name:          "WSL環境でのbash判定",
			shell:         "/bin/bash",
			wslDistro:     "Ubuntu",
			psModulePath:  "",
			expectedShell: "bash",
			description:   "WSL環境ではSHELL環境変数を優先",
		},
		{
			name:          "WSL環境でのzsh判定",
			shell:         "/usr/bin/zsh",
			wslDistro:     "Ubuntu",
			psModulePath:  "",
			expectedShell: "zsh",
			description:   "WSL環境でzshを検出",
		},
		{
			name:          "WSL環境でのfish判定",
			shell:         "/usr/bin/fish",
			wslDistro:     "Ubuntu",
			psModulePath:  "",
			expectedShell: "fish",
			description:   "WSL環境でfishを検出",
		},
		{
			name:          "非WSL環境でのbash判定",
			shell:         "/bin/bash",
			wslDistro:     "",
			psModulePath:  "",
			expectedShell: func() string {
				if runtime.GOOS == "windows" {
					return "powershell"
				}
				return "bash"
			}(),
			description:   "非WSL環境でのシェル判定（ビルド環境に依存）",
		},
		{
			name:          "PowerShell環境の判定",
			shell:         "",
			wslDistro:     "",
			psModulePath:  "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\Modules",
			expectedShell: "powershell",
			description:   "PowerShell環境を検出",
		},
		{
			name:          "未知のシェル環境",
			shell:         "/unknown/shell",
			wslDistro:     "",
			psModulePath:  "",
			expectedShell: func() string {
				if runtime.GOOS == "windows" {
					return "powershell"
				}
				return "bash"
			}(),
			description:   "未知の環境でのデフォルト判定（ビルド環境に依存）",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 環境変数を設定
			os.Setenv("SHELL", tt.shell)
			os.Setenv("WSL_DISTRO_NAME", tt.wslDistro)
			os.Setenv("PSModulePath", tt.psModulePath)
			
			// シェル判定を実行
			detectedShell := detectCurrentShell()
			
			if detectedShell != tt.expectedShell {
				t.Errorf("シェル判定失敗: 期待=%s, 実際=%s (%s)",
					tt.expectedShell, detectedShell, tt.description)
			}
			
			t.Logf("✓ %s: %s", tt.name, tt.description)
		})
	}
}

// =============================================================================
// 補完スクリプト生成機能のテスト
// =============================================================================

func TestGenerateCompletionScript(t *testing.T) {
	tests := []struct {
		shell       string
		shouldError bool
		description string
	}{
		{
			shell:       "bash",
			shouldError: false,
			description: "bash用補完スクリプト生成",
		},
		{
			shell:       "zsh",
			shouldError: false,
			description: "zsh用補完スクリプト生成",
		},
		{
			shell:       "fish",
			shouldError: false,
			description: "fish用補完スクリプト生成",
		},
		{
			shell:       "powershell",
			shouldError: false,
			description: "powershell用補完スクリプト生成",
		},
		{
			shell:       "pwsh",
			shouldError: false,
			description: "pwsh用補完スクリプト生成",
		},
		{
			shell:       "unsupported",
			shouldError: true,
			description: "サポートされていないシェル",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			// 標準出力をキャプチャするために一時的にリダイレクト
			oldStdout := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w
			
			var output bytes.Buffer
			done := make(chan bool)
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := r.Read(buf)
					if n > 0 {
						output.Write(buf[:n])
					}
					if err != nil {
						break
					}
				}
				done <- true
			}()
			
			// 補完スクリプト生成を実行
			err := generateCompletionScript(tt.shell)
			
			// 標準出力を復元
			w.Close()
			os.Stdout = oldStdout
			<-done
			r.Close()
			
			// 結果を検証
			if tt.shouldError {
				if err == nil {
					t.Errorf("エラーが期待されましたが成功しました: %s", tt.description)
				}
			} else {
				if err != nil {
					t.Errorf("補完スクリプト生成エラー: %v (%s)", err, tt.description)
				}
				
				// 出力内容の簡単な検証
				outputStr := output.String()
				if len(outputStr) == 0 {
					t.Errorf("補完スクリプトの出力が空です: %s", tt.description)
				}
				
				// 各シェル固有の内容があることを確認
				switch tt.shell {
				case "bash":
					if !strings.Contains(outputStr, "bash") {
						t.Errorf("bash用補完スクリプトにbash固有の内容が含まれていません")
					}
				case "zsh":
					if !strings.Contains(outputStr, "zsh") {
						t.Errorf("zsh用補完スクリプトにzsh固有の内容が含まれていません")
					}
				case "fish":
					if !strings.Contains(outputStr, "complete") {
						t.Errorf("fish用補完スクリプトにfish固有の内容が含まれていません")
					}
				case "powershell", "pwsh":
					if !strings.Contains(outputStr, "Register-ArgumentCompleter") {
						t.Errorf("PowerShell用補完スクリプトにPowerShell固有の内容が含まれていません")
					}
				}
			}
			
			t.Logf("✓ %s: %s", tt.shell, tt.description)
		})
	}
}

// =============================================================================
// 設定ファイル読み込みのテスト
// =============================================================================

func TestLoadConfigDryRun(t *testing.T) {
	// テスト用ディレクトリ作成
	tempDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("一時ディレクトリの作成に失敗: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	tests := []struct {
		name        string
		configData  string
		expectedDry bool
		shouldError bool
		description string
	}{
		{
			name: "dry_run=true設定",
			configData: `{
				"dry_run": true,
				"work_dir": "/test/work",
				"backup_dir": "/test/backup"
			}`,
			expectedDry: true,
			shouldError: false,
			description: "dry_runがtrueに設定されている",
		},
		{
			name: "dry_run=false設定",
			configData: `{
				"dry_run": false,
				"work_dir": "/test/work",
				"backup_dir": "/test/backup"
			}`,
			expectedDry: false,
			shouldError: false,
			description: "dry_runがfalseに設定されている",
		},
		{
			name: "HJSON形式のdry_run=true",
			configData: `{
				// コメント付きの設定
				dry_run: true,
				work_dir: "/test/work",
				backup_dir: "/test/backup",
			}`,
			expectedDry: true,
			shouldError: false,
			description: "HJSON形式でdry_runがtrueに設定",
		},
		{
			name: "HJSON形式のdry_run=false",
			configData: `{
				// 実行モード設定
				dry_run: false,
				work_dir: "/test/work",
				backup_dir: "/test/backup",
			}`,
			expectedDry: false,
			shouldError: false,
			description: "HJSON形式でdry_runがfalseに設定",
		},
		{
			name: "不正なHJSON構文",
			configData: `{
				dry_run: true,
				work_dir: "/test/work",
				backup_dir: "/test/backup",
				invalid_syntax: [broken
			}`,
			expectedDry: false,
			shouldError: true,
			description: "構文エラーのあるHJSON",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// テスト用設定ファイルを作成
			configPath := filepath.Join(tempDir, fmt.Sprintf("config_%s.hjson", tt.name))
			err := os.WriteFile(configPath, []byte(tt.configData), 0644)
			if err != nil {
				t.Fatalf("テスト設定ファイルの作成に失敗: %v", err)
			}
			
			// 設定を読み込み
			cfg, err := loadConfig(configPath)
			
			if tt.shouldError {
				if err == nil {
					t.Errorf("エラーが期待されましたが成功しました: %s", tt.description)
				}
				return
			}
			
			if err != nil {
				t.Fatalf("設定読み込みエラー: %v (%s)", err, tt.description)
			}
			
			// dry_runの値を検証
			if cfg.DryRun != tt.expectedDry {
				t.Errorf("dry_run設定が間違っています: 期待=%v, 実際=%v (%s)",
					tt.expectedDry, cfg.DryRun, tt.description)
			}
			
			t.Logf("✓ %s: dry_run=%v (%s)", tt.name, cfg.DryRun, tt.description)
		})
	}
}

// =============================================================================
// バックアップタイミング判定のテスト
// =============================================================================

func TestDetermineBestBackupLevelTiming(t *testing.T) {
	tests := []struct {
		name        string
		time        time.Time
		shouldRun   bool
		level       string
		description string
	}{
		{
			name:        "30分タイミング_10:30",
			time:        time.Date(2025, 7, 1, 10, 30, 0, 0, time.Local),
			shouldRun:   true,
			level:       "30m",
			description: "30分間隔バックアップの実行タイミング",
		},
		{
			name:        "30分タイミング_14:00",
			time:        time.Date(2025, 7, 1, 14, 0, 0, 0, time.Local),
			shouldRun:   true,
			level:       "30m",
			description: "正時の30分間隔バックアップ（他の条件と重複しない時間）",
		},
		{
			name:        "非タイミング_10:15",
			time:        time.Date(2025, 7, 1, 10, 15, 0, 0, time.Local),
			shouldRun:   false,
			level:       "",
			description: "バックアップタイミング外",
		},
		{
			name:        "非タイミング_10:45",
			time:        time.Date(2025, 7, 1, 10, 45, 0, 0, time.Local),
			shouldRun:   false,
			level:       "",
			description: "バックアップタイミング外",
		},
		{
			name:        "3時間タイミング_09:00",
			time:        time.Date(2025, 7, 1, 9, 0, 0, 0, time.Local),
			shouldRun:   true,
			level:       "3h",
			description: "3時間間隔バックアップの実行タイミング",
		},
		{
			name:        "6時間タイミング_06:00",
			time:        time.Date(2025, 7, 1, 6, 0, 0, 0, time.Local),
			shouldRun:   true,
			level:       "6h",
			description: "6時間間隔バックアップの実行タイミング",
		},
		{
			name:        "12時間タイミング_12:00",
			time:        time.Date(2025, 7, 1, 12, 0, 0, 0, time.Local),
			shouldRun:   true,
			level:       "12h",
			description: "12時間間隔バックアップの実行タイミング",
		},
		{
			name:        "1日タイミング_00:00",
			time:        time.Date(2025, 7, 1, 0, 0, 0, 0, time.Local),
			shouldRun:   true,
			level:       "1d",
			description: "1日間隔バックアップの実行タイミング（最優先）",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldRun, level := determineBestBackupLevel(tt.time)
			
			if shouldRun != tt.shouldRun {
				t.Errorf("バックアップ実行判定が間違っています: 期待=%v, 実際=%v (%s)",
					tt.shouldRun, shouldRun, tt.description)
			}
			
			if level != tt.level {
				t.Errorf("バックアップレベルが間違っています: 期待=%s, 実際=%s (%s)",
					tt.level, level, tt.description)
			}
			
			t.Logf("✓ %s: shouldRun=%v, level=%s (%s)",
				tt.name, shouldRun, level, tt.description)
		})
	}
}
// =============================================================================
// ログバッファリング機能のテスト  
// =============================================================================

func TestLogBuffer(t *testing.T) {
	// LogBuffer のテスト
	buffer := &LogBuffer{
		buffer: &bytes.Buffer{},
	}
	
	// データを書き込み
	testData := "テストログメッセージ\n"
	n, err := buffer.Write([]byte(testData))
	if err != nil {
		t.Fatalf("LogBuffer.Write() エラー: %v", err)
	}
	if n != len(testData) {
		t.Errorf("書き込みバイト数が違います: 期待=%d, 実際=%d", len(testData), n)
	}
	
	// データを取得してクリア
	data := buffer.GetAndClear()
	if string(data) != testData {
		t.Errorf("取得データが違います: 期待=%s, 実際=%s", testData, string(data))
	}
	
	// クリア後は空であることを確認
	data2 := buffer.GetAndClear()
	if len(data2) != 0 {
		t.Errorf("クリア後にデータが残っています: %s", string(data2))
	}
}

func TestMultiWriter(t *testing.T) {
	// MultiWriter のテスト
	var buf1, buf2 bytes.Buffer
	
	mw := &MultiWriter{
		writers: []io.Writer{&buf1, &buf2},
	}
	
	// データを書き込み
	testData := "マルチライターテスト\n"
	n, err := mw.Write([]byte(testData))
	if err != nil {
		t.Fatalf("MultiWriter.Write() エラー: %v", err)
	}
	if n != len(testData) {
		t.Errorf("書き込みバイト数が違います: 期待=%d, 実際=%d", len(testData), n)
	}
	
	// 両方のバッファにデータが書き込まれているか確認
	if buf1.String() != testData {
		t.Errorf("buf1のデータが違います: 期待=%s, 実際=%s", testData, buf1.String())
	}
	if buf2.String() != testData {
		t.Errorf("buf2のデータが違います: 期待=%s, 実際=%s", testData, buf2.String())
	}
}

func TestSetupLogOutput(t *testing.T) {
	// setupLogOutput のテスト（ファイル作成なしでテスト）
	cfg := &BackupConfig{
		LogFile: "", // ログファイル未設定
	}
	
	// ログファイル未設定の場合はエラーなしで終了
	err := setupLogOutput(cfg)
	if err != nil {
		t.Errorf("ログファイル未設定でエラーが発生: %v", err)
	}
	
	// 実際のファイル作成テストは統合テストで行う
	t.Log("setupLogOutput() の基本動作テスト完了")
}
