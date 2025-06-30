package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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