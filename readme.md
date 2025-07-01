# rotate_backup

VHDXファイルの高度バックアップローテーション管理ツール

## 概要

`rotate_backup`は、VHDX内のファイルを対象とした高度なバックアップローテーション管理ツールです。拡張子フィルタリング、多段階ローテーション、インテリジェントなコピー方式選択により、効率的で確実なバックアップを実現します。

## ✨ 主な機能

### 🔄 **スマートコピーシステム**
- **拡張子フィルタリング**: 指定拡張子のファイルのみをバックアップ
- **2段階robocopy**: 拡張子フィルタ + ミラーリング（不要ファイル削除）
- **自動フォールバック**: robocopy → xcopy → PowerShell → native
- **方式自動補完**: 設定されていないコピー方式を自動追加

### 📊 **多段階ローテーション**
- **5段階の独立バックアップ**: 30分、3時間、6時間、12時間、1日間隔
- **間隔別スナップショット**: 各間隔で独立してバックアップを作成
- **カスタマイズ可能**: 各レベルの保持数を個別設定

### 🛡️ **高信頼性機能**
- **VHDX自動マウント**: PowerShell経由での自動マウント
- **多重実行防止**: ファイルロックによる排他制御
- **包括的ログ**: 実行ログ + パフォーマンスログ（TSV形式）
- **エラーハンドリング**: Windowsシステムフォルダ自動除外

### 🔔 **通知システム**
- **Windowsトースト通知**: go-toastライブラリ使用
- **個別設定**: 通知タイプ別ON/OFF（開始/完了/エラー/多重実行検出）
- **フォールバック**: msg.exe による代替通知

### ⚡ **運用モード**
- **定期起動モード**（デフォルト）: 外部スケジューラから起動、処理後即終了
- **常駐モード** (`--daemon`): 内部スケジューラでcron的動作、継続実行
- **通常バックアップ**: フル機能（コピー + VHDX保存 + ローテーション）
- **更新モード** (`--update-backup`): コピー処理のみの高速実行
- **ドライランモード**: 実処理なしの安全なシミュレーション

## 🚀 インストール

### 前提条件

- **Go 1.23.0以降** （ビルド時のみ）
- **Windows 10/11** （PowerShell、robocopy対応）
- **管理者権限** （VHDX操作時に推奨）

### ビルド

```bash
# 推奨：Makefileを使用
make build

# 手動ビルド
GOOS=windows go build -o rotate_backup.exe
```

### 依存関係

- [github.com/go-toast/toast](https://github.com/go-toast/toast) - Windowsトースト通知
- [github.com/alexflint/go-arg](https://github.com/alexflint/go-arg) - コマンドライン引数解析
- [github.com/hjson/hjson-go](https://github.com/hjson/hjson-go) - HJSON設定ファイル
- [golang.org/x/text](https://pkg.go.dev/golang.org/x/text) - 文字エンコーディング変換

## 📋 使用方法

### クイックスタート

```bash
# 1. 設定ファイルテンプレートを生成
rotate_backup.exe --init-config

# 2. config.hjsonを編集（パス設定など）

# 3. ドライラン実行で動作確認
rotate_backup.exe  # dry_run: true で安全確認

# 4. 設定ファイルでdry_run: falseに変更

# 5. 実際のバックアップ実行
rotate_backup.exe
```

### コマンドライン使用法

```bash
rotate_backup.exe [サブコマンド] [オプション]
```

#### サブコマンド

| サブコマンド | 説明 |
|-------------|------|
| （なし） | 定期起動モードで実行（デフォルト） |
| `daemon` | **NEW** 常駐モードで起動（内部スケジューラ使用） |
| `init` | 設定テンプレートを生成 |

#### グローバルオプション

| オプション | 短縮形 | 説明 |
|-----------|--------|------|
| `--config <path>` | `-c` | 設定ファイルのパス (default: config.hjson) |
| `--update-backup` | `-u` | コピー処理のみ実行（高速モード） |
| `--help` | `-h` | ヘルプを表示 |
| `--version` | `-v` | バージョンを表示 |

#### daemonサブコマンド専用オプション
| オプション | 説明 |
|-----------|------|
| `--pid-file <path>` | PIDファイルのパス (default: rotate_backup.pid) |
| `--log-level <level>` | ログレベル (debug/info/warn/error) |

### 運用例

```bash
# 定期起動モード（デフォルト）
# タスクスケジューラで30分ごとに実行
rotate_backup.exe

# 常駐モードで起動
rotate_backup.exe daemon
rotate_backup.exe daemon --pid-file C:/var/run/backup.pid
rotate_backup.exe daemon --log-level debug

# Windowsサービスとして登録（常駐モード）
sc create RotateBackup binPath= "C:\path\to\rotate_backup.exe daemon"

# 設定ファイルの初期化
rotate_backup.exe init
rotate_backup.exe init --config custom.hjson

# 高速更新（コピーのみ、ローテーションなし）
rotate_backup.exe --update-backup

# 別の設定ファイルを使用
rotate_backup.exe --config production.hjson

# ドライラン確認
rotate_backup.exe  # config.hjsonでdry_run: trueの場合
```

## ⚙️ 設定ファイル

### 設定ファイル生成

```bash
# 詳細なコメント付きテンプレートを生成
rotate_backup.exe init

# 特定のパスに生成
rotate_backup.exe init --config C:\MyBackups\custom.hjson
```

### 主要設定項目

#### 🔧 **基本設定**
```hjson
{
  // 実行モード（trueで安全なシミュレーション）
  dry_run: true

  // パス設定
  work_dir: "P:/"                              // コピー元（VHDXマウント先）
  backup_dir: "Q:/"                            // コピー先
  source_vhdx: "C:/Backups/backup.vhdx"        // バックアップするVHDX
  vhdx_mount_drive: "Q:"                       // VHDXマウント先ドライブ
  mount_vhdx_if_missing: true                  // 未マウント時の自動マウント
}
```

#### 🎯 **拡張子フィルタリング**
```hjson
{
  // バックアップ対象拡張子（空=全ファイル）
  extensions: [".cpp", ".hpp", ".c", ".h", ".txt", ".md"]
  
  // 除外ディレクトリ（Windowsシステムフォルダは自動除外）
  exclude_dirs: [
    "P:/Temp",
    "P:/Debug", 
    "P:/node_modules"
  ]
}
```

#### 🔄 **コピー方式設定**
```hjson
{
  // 優先順位（不足分は自動補完）
  copy_method_priority: ["robocopy", "xcopy", "copy-item", "native"]
  
  // 各方式の引数
  copy_args: {
    // robocopy: 拡張子フィルタ時は2段階実行
    robocopy: "/MIR /R:1 /W:1 /NJH /NJS /NP"
    // xcopy: 拡張子フィルタ時は個別ファイルコピー  
    xcopy: "/E /Y /D /H"
    // copy-item: PowerShell、拡張子フィルタリング対応
    copy-item: "-Recurse -Force"
    // native: Go言語内蔵（最終手段）
    native: ""
  }
}
```

#### 📊 **ローテーション設定**
```hjson
{
  // 各レベルの保持数（各間隔で独立してバックアップを作成）
  keep_versions: {
    "30m": 5,    // 30分ごとにバックアップ、最大5個保持 = 2.5時間分
    "3h": 2,     // 3時間ごとにバックアップ、最大2個保持 = 6時間分  
    "6h": 2,     // 6時間ごとにバックアップ、最大2個保持 = 12時間分
    "12h": 2,    // 12時間ごとにバックアップ、最大2個保持 = 24時間分
    "1d": 5      // 1日ごとにバックアップ、最大5個保持 = 5日分
  }

  // 各レベルのディレクトリ（それぞれの間隔でバックアップを保存）
  backup_dirs: {
    "30m": "C:/Backups/30m",  // 30分間隔のバックアップ
    "3h":  "C:/Backups/3h",   // 3時間間隔のバックアップ
    "6h":  "C:/Backups/6h",   // 6時間間隔のバックアップ
    "12h": "C:/Backups/12h",  // 12時間間隔のバックアップ
    "1d":  "C:/Backups/1d"    // 1日間隔のバックアップ
  }
}
```

#### 🔔 **通知設定**
```hjson
{
  // 各通知のON/OFF個別設定
  notifications: {
    lock_conflict: true,   // 多重実行検出（重要）
    backup_start: false,   // バックアップ開始（通常不要）
    backup_end: true,      // バックアップ完了（推奨）
    update_end: false,     // --update-backup完了（頻繁実行時は無効推奨）
    error: true           // エラー発生（重要）
  }
}
```

#### 📝 **ログ・多重実行防止**
```hjson
{
  // ログ設定
  log_file: "C:/Backups/log.txt",           // 実行ログ
  perf_log_path: "C:/Backups/perf.tsv",     // パフォーマンスログ（TSV）

  // 多重実行防止
  enable_lock: true,                        // ファイルロック有効
  lock_file_path: "C:/Backups/backup.lock", // ロックファイルパス
  on_lock_conflict: "notify-exit"           // 競合時の動作
}
```

## 🔄 ローテーション仕組み

### 間隔別独立バックアップ方式

各レベルのフォルダは**独立して**、その間隔でバックアップを作成・保持します：

1. **30分間隔** (`30m`): 30分ごとにバックアップを作成、最大5個保持
2. **3時間間隔** (`3h`): 3時間ごとにバックアップを作成、最大2個保持
3. **6時間間隔** (`6h`): 6時間ごとにバックアップを作成、最大2個保持
4. **12時間間隔** (`12h`): 12時間ごとにバックアップを作成、最大2個保持
5. **1日間隔** (`1d`): 1日ごとにバックアップを作成、最大5個保持

### 動作モード

#### 定期起動モード（デフォルト）
- **概要**: 外部スケジューラ（Windowsタスクスケジューラ等）から定期的に起動
- **動作**: 起動 → 現在時刻確認 → 必要なバックアップ実行 → 終了
- **メリット**: メモリ使用量最小、障害時の自動復旧、設定変更が即座に反映
- **設定例**: タスクスケジューラで30分ごとに`rotate_backup.exe`を実行

#### 常駐モード
- **概要**: プログラム内部でスケジューリングし、継続的に動作
- **動作**: 起動 → スケジューラ初期化 → 定刻実行を繰り返し → 終了信号で停止
- **メリット**: より正確な時刻実行、外部スケジューラ不要
- **起動方法**: `rotate_backup.exe daemon`

### バックアップ作成タイミング（時刻ベース実行）

- **30分バックアップ**: 毎時00分、30分（例: 09:00, 09:30, 10:00...）
- **3時間バックアップ**: 0,3,6,9,12,15,18,21時（例: 09:00, 12:00, 15:00...）
- **6時間バックアップ**: 0,6,12,18時（例: 06:00, 12:00, 18:00, 00:00）
- **12時間バックアップ**: 0,12時（例: 00:00, 12:00）
- **1日バックアップ**: 毎日0時（例: 00:00）

#### 実行タイミングの特徴

- **時刻ベース実行**: 予測可能な定刻実行（cron的動作）
- **排他的バックアップ**: 同時刻では最長間隔のバックアップのみ実行
  - 例: 00:00時は1dのみ、12:00時は12hのみ、09:00時は3hのみ
- **初回実行**: プログラム起動時、次の実行時刻が1時間以上先の場合は即座に実行
- **キャッチアップ**: システム停止等で逃したバックアップは起動時に実行

### 保持期間

各レベルで保持数を超えた場合、最古のファイルから削除されます：

### 保持期間の計算例

| レベル | 間隔 | 保持数 | 実質保持期間 |
|--------|------|--------|-------------|
| 30m | 30分 | 5個 | 2.5時間 |
| 3h | 3時間 | 2個 | 6時間 |
| 6h | 6時間 | 2個 | 12時間 |
| 12h | 12時間 | 2個 | 24時間 |
| 1d | 1日 | 5個 | 5日間 |

## 📊 パフォーマンスログ

### TSV形式でのパフォーマンス記録

`perf_log_path`で指定されたファイルに、タブ区切り形式で実行時間を記録：

```tsv
2025-06-13T10:30:00+09:00	1718245800000	4500	1200	500
2025-06-13T11:00:00+09:00	1718247600000	3200	800	300
```

| 列 | 内容 | 用途 |
|---|---|---|
| 1列目 | 実行日時(ISO 8601) | 実行タイミングの特定 |
| 2列目 | UNIXミリ秒(開始時刻) | 数値解析用タイムスタンプ |
| 3列目 | 全体処理時間(ms) | 総実行時間の把握 |
| 4列目 | コピー処理時間(ms) | コピー性能の監視 |
| 5列目 | ローテーション処理時間(ms) | ローテーション効率の確認 |

### 分析例

```bash
# Excel等でTSVファイルを開いてグラフ化
# PowerShellでの簡易分析例
Import-Csv C:/Backups/perf.tsv -Delimiter "`t" -Header "DateTime","Unix","Total","Copy","Rotate"
```

## 🔄 コピー方式の詳細

### 自動フォールバック機能

優先順位に従って最初に利用可能なコマンドを使用。不足している方式は自動補完：

| 優先順位 | 方式 | 特徴 | 拡張子フィルタ対応 |
|----------|------|------|-------------------|
| 1 | **robocopy** | 高速・高機能、ミラーリング | ✅ 2段階実行 |
| 2 | **xcopy** | Windows標準、安定性重視 | ✅ 個別ファイルコピー |
| 3 | **copy-item** | PowerShell、柔軟性が高い | ✅ スクリプト処理 |
| 4 | **native** | Go内蔵、確実に動作 | ✅ クロスプラットフォーム |

### 拡張子フィルタリング時の動作

#### robocopy（2段階実行）
```bash
# 段階1: 指定拡張子ファイルをコピー
robocopy P:/ Q:/ *.cpp *.hpp /E /R:1 /W:1
# 段階2: 不要ファイルを削除（Go言語で実装）
```

#### xcopy（個別ファイル処理）
```bash
# 対象ファイルを事前に探索してから個別コピー
xcopy P:/src/main.cpp Q:/src/main.cpp /Y /D
```

#### PowerShell（スクリプト処理）
```powershell
# 拡張子フィルタとコピーを一体化
Get-ChildItem -Recurse | Where-Object {$_.Extension -eq '.cpp'} | Copy-Item
```

## 🔒 多重実行防止

### ファイルロックによる排他制御

`enable_lock: true`の場合、ロックファイルにより同時実行を防止：

#### 動作仕組み
- **開始時**: `backup.lock`ファイルを作成、現在のPIDを記録
- **実行中**: 他のプロセスは開始時にロックファイル存在をチェック
- **終了時**: 正常終了時にロックファイルを自動削除
- **異常終了**: 手動でロックファイルを削除する必要がある場合あり

#### 競合時の動作
```
競合検出 → 通知送信 → 処理終了（exit）
```

#### トラブル対応
```bash
# ロックファイルの手動確認・削除
dir C:\Backups\backup.lock
del C:\Backups\backup.lock
```

#### 設定例
```hjson
{
  enable_lock: true,
  lock_file_path: "C:/Backups/backup.lock",
  on_lock_conflict: "notify-exit",  // 現在は notify-exit のみサポート
  notifications: {
    lock_conflict: true  // 競合時通知を有効化（推奨）
  }
}
```

## 🛠️ トラブルシューティング

### よくある問題と解決方法

#### 1. 設定ファイル関連

**問題**: `config.hjson not found`
```bash
# 解決方法
rotate_backup.exe --init-config
# または
rotate_backup.exe -i
```

**問題**: HJSON構文エラー
```hjson
# 正しい例（コメントとカンマOK）
{
  dry_run: true,  // コメント可能
  work_dir: "P:/"
}
```

#### 2. VHDX・マウント関連

**問題**: VHDXマウントに失敗
```powershell
# PowerShell実行ポリシー確認
Get-ExecutionPolicy
# 必要に応じて変更（管理者権限）
Set-ExecutionPolicy RemoteSigned
```

**問題**: ドライブがマウントされない
- 管理者権限で実行
- VHDXファイルのパスを確認
- ディスクの管理で手動マウント確認

#### 3. コピー・権限関連

**問題**: アクセス拒否エラー
```bash
# 管理者権限で実行
# または
# 除外ディレクトリに問題のあるフォルダを追加
```

**問題**: 拡張子フィルタリングが効かない
- 拡張子の大文字・小文字を確認
- ドライランで対象ファイル数を確認
- `extensions: []` で全ファイルコピーをテスト

#### 4. 通知関連

**問題**: トースト通知が表示されない
```hjson
# フォールバック通知の確認
notifications: {
  error: true,
  backup_end: true
}
```

#### 5. 多重実行防止

**問題**: ロックファイルエラー
```bash
# ロックファイルを手動削除
del "C:/Backups/backup.lock"
# PID確認（必要に応じて）
type "C:/Backups/backup.lock"
```

### 🧪 ドライランでの事前確認

実際の処理を行う前に、**必ず**ドライランで動作を確認：

```bash
# 1. 設定ファイルでdry_run: trueを確認
# 2. ドライラン実行
rotate_backup.exe

# 3. 出力例の確認項目
# - コピー対象ファイル数
# - 使用されるコピー方式
# - 除外ディレクトリの動作
# - ローテーション動作
# - 通知設定

# 4. 問題なければ dry_run: false に変更
# 5. 本番実行
```

### 📝 ログ確認方法

```bash
# 実行ログの確認
type "C:/Backups/log.txt"

# パフォーマンスログの確認（TSV）
type "C:/Backups/perf.tsv"

# 最新のエラーを確認
findstr "Error\|失敗\|エラー" "C:/Backups/log.txt"
```

### 🔧 高度なデバッグ

```bash
# より詳細なログが必要な場合
rotate_backup.exe --config debug.hjson 2>&1 | tee debug.log

# 特定のコピー方式のテスト
# copy_method_priority: ["xcopy"] など1つに限定
```

## ライセンス

このプロジェクトはMITライセンスの下で公開されています。

## 依存関係

- [github.com/alexflint/go-arg](https://github.com/alexflint/go-arg) - コマンドライン引数解析
- [github.com/hjson/hjson-go](https://github.com/hjson/hjson-go) - HJSON設定ファイル解析
- [github.com/pkg/errors](https://github.com/pkg/errors) - エラーハンドリング