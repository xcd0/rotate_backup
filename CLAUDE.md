# rotate_backup プロジェクト - Claude Code 設定

## プロジェクト概要
Windows用VHDXバックアップローテーション管理ツール - WSL環境での開発、Windows実行環境を想定

## 開発環境・ビルド
```bash
# ビルド
make build

# テスト実行
GOOS=windows go test -v

# 特定テストのみ実行
GOOS=windows go test -v -run TestExtensionFiltering
```

## プロジェクト固有の重要事項

### 重要なファイル
- `config.hjson`: HJSON形式設定ファイル
- `makefile`: ビルド設定（小文字）
- `main.go`: メインプログラム
- `main_test.go`: テストファイル

### Windows専用機能
- go-toastライブラリ（Windows通知）
- PowerShell経由でのVHDX操作
- robocopy、xcopy等Windowsコマンド使用

### テスト方針
- 拡張子フィルタリングの包括的テスト
- 複数拡張子、大文字小文字、パス付きファイル名等のエッジケース
- 実際のファイルシステムでのテスト

## 注意事項
- GOOS=windowsでのビルド必須
- WSL環境でWindows向けexeを直接実行可能
- 管理者権限推奨（VHDX操作時）
- Linuxネイティブ機能は使用禁止