package commands

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestEnvironment creates a temporary directory structure for testing
func setupTestEnvironment(t *testing.T) (sourceDir, targetBaseDir, workDir string) {
	tmpBase := t.TempDir()
	sourceDir = filepath.Join(tmpBase, "source")
	targetBaseDir = filepath.Join(tmpBase, "target")
	workDir = filepath.Join(tmpBase, "work")

	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.MkdirAll(targetBaseDir, 0o755))
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	return sourceDir, targetBaseDir, workDir
}

// executeScript runs the generated bash script and returns the result
func executeScript(t *testing.T, script string, workDir string) (stdout, stderr string, exitCode int) {
	scriptFile := filepath.Join(workDir, "test_script.sh")
	err := os.WriteFile(scriptFile, []byte(script), 0o755)
	require.NoError(t, err, "Failed to write script file")

	cmd := exec.Command("/bin/bash", scriptFile)
	cmd.Dir = workDir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("Failed to execute script: %v", err)
		}
	} else {
		exitCode = 0
	}

	return stdout, stderr, exitCode
}

// getCurrentUser returns current user and group for testing
func getCurrentUser(t *testing.T) (uid, gid int) {
	uid = os.Getuid()
	gid = os.Getgid()
	return uid, gid
}

// getFilePermissions returns the permission bits of a file
func getFilePermissions(t *testing.T, path string) os.FileMode {
	info, err := os.Stat(path)
	require.NoError(t, err, "Failed to stat file")
	return info.Mode().Perm()
}

// renderTemplate is a helper to render the template
func renderTemplate(t *testing.T, data copyScriptData) string {
	var buf bytes.Buffer
	err := copyScriptTemplate.Execute(&buf, data)
	require.NoError(t, err, "Template execution should not fail")
	return buf.String()
}

// createFilesAndDirs creates files, directories, and symlinks from a map
// Values: "file", "dir", "symlink"
func createFilesAndDirs(t *testing.T, baseDir string, paths map[string]string) {
	for path, entryType := range paths {
		fullPath := filepath.Join(baseDir, path)

		switch entryType {
		case "dir":
			require.NoError(t, os.MkdirAll(fullPath, 0o755))
		case "file":
			// Ensure parent dir exists
			dir := filepath.Dir(fullPath)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(fullPath, []byte("dummy"), 0o644))
		case "symlink":
			// Create symlink target outside the tree
			dir := filepath.Dir(fullPath)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			targetFile := filepath.Join(baseDir, "..", "symlink_target_"+filepath.Base(path))
			require.NoError(t, os.WriteFile(targetFile, []byte("symlink target"), 0o644))
			require.NoError(t, os.Symlink(targetFile, fullPath))
		default:
			t.Fatalf("Unknown entry type: %s", entryType)
		}
	}
}

// verifyFilesAndDirs verifies files, directories, and symlinks exist
func verifyFilesAndDirs(t *testing.T, baseDir string, paths map[string]string) {
	for path, entryType := range paths {
		fullPath := filepath.Join(baseDir, path)

		switch entryType {
		case "dir":
			assert.DirExists(t, fullPath, "Directory %s should exist", path)
		case "file":
			assert.FileExists(t, fullPath, "File %s should exist", path)
		case "symlink":
			info, err := os.Lstat(fullPath)
			require.NoError(t, err, "Symlink %s should exist", path)
			assert.Equal(t, os.ModeSymlink, info.Mode()&os.ModeSymlink, "%s should be a symlink", path)
		default:
			t.Fatalf("Unknown entry type: %s", entryType)
		}
	}
}

// testCase defines a comprehensive test scenario
type testCase struct {
	name        string
	description string

	// Setup: map of paths to their types
	// Types: "file", "dir", "symlink"
	// Example: {"app/": "dir", "app/main.js": "file", "link": "symlink"}
	files map[string]string

	// Input: the path within the extracted files to copy from
	// Examples: "." (root), "app/" (subdirectory), "src/main.js" (specific file)
	copyFrom string

	// Input: where to copy to (relative or absolute)
	copyTo string

	// Optional: permissions to apply
	permissions string

	// Optional: owner (will be set to current user:group if empty)
	owner string

	// Expected results
	shouldSucceed    bool
	expectedExitCode int
	expectedError    string // substring to look for in stderr

	// Verification: what paths to check in the target with their types
	expectedPaths map[string]string
}

func TestCopyScriptBehavior(t *testing.T) {
	uid, gid := getCurrentUser(t)
	currentUser := fmt.Sprintf("%d:%d", uid, gid)

	tests := []testCase{
		{
			name:        "single_file_root_level",
			description: "Single file at root, copied to target file path",
			files: map[string]string{
				"test.txt": "file",
			},
			copyFrom:      ".",
			copyTo:        "/dest/copied.txt",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/copied.txt": "file",
			},
		},
		{
			name:        "multiple_files_root_level",
			description: "Multiple files at root, copied to target directory",
			files: map[string]string{
				"file1.txt": "file",
				"file2.txt": "file",
			},
			copyFrom:      ".",
			copyTo:        "/dest/",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/file1.txt": "file",
				"dest/file2.txt": "file",
			},
		},
		{
			name:        "directory",
			description: "Nested app/ directory with subdirectories",
			files: map[string]string{
				"app/":                    "dir",
				"app/main.js":             "file",
				"app/src/":                "dir",
				"app/src/index.js":        "file",
				"app/src/utils/":          "dir",
				"app/src/utils/helper.js": "file",
			},
			copyFrom:      ".",
			copyTo:        "/work/",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"work/app/main.js":             "file",
				"work/app/src/":                "dir",
				"work/app/src/index.js":        "file",
				"work/app/src/utils/":          "dir",
				"work/app/src/utils/helper.js": "file",
			},
		},
		{
			name:        "nested_directory_structure_to_target",
			description: "Nested app/ directory with subdirectories",
			files: map[string]string{
				"app/":                    "dir",
				"app/main.js":             "file",
				"app/src/":                "dir",
				"app/src/index.js":        "file",
				"app/src/utils/":          "dir",
				"app/src/utils/helper.js": "file",
			},
			copyFrom:      "app/",
			copyTo:        "/app/",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"app/main.js":             "file",
				"app/src/":                "dir",
				"app/src/index.js":        "file",
				"app/src/utils/":          "dir",
				"app/src/utils/helper.js": "file",
			},
		},
		{
			name:        "copy_from_deeply_nested_subfolder",
			description: "Copy from a deeply nested subfolder within source",
			files: map[string]string{
				"project/":                          "dir",
				"project/src/":                      "dir",
				"project/src/components/":           "dir",
				"project/src/components/Button.tsx": "file",
				"project/src/components/Input.tsx":  "file",
				"project/src/utils/":                "dir",
				"project/src/utils/helpers.ts":      "file",
			},
			copyFrom:      "project/src/components/",
			copyTo:        ".",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"Button.tsx": "file",
				"Input.tsx":  "file",
			},
		},
		{
			name:        "copy_from_nested_folder_with_subdirs",
			description: "Copy from nested folder that itself contains subdirectories",
			files: map[string]string{
				"project/":                         "dir",
				"project/tests/":                   "dir",
				"project/tests/unit/":              "dir",
				"project/tests/unit/test1.ts":      "file",
				"project/tests/unit/test2.ts":      "file",
				"project/tests/integration/":       "dir",
				"project/tests/integration/api.ts": "file",
			},
			copyFrom:      "project/tests/",
			copyTo:        "/test-suite/",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"test-suite/unit/":              "dir",
				"test-suite/unit/test1.ts":      "file",
				"test-suite/unit/test2.ts":      "file",
				"test-suite/integration/":       "dir",
				"test-suite/integration/api.ts": "file",
			},
		},
		{
			name:        "relative_target_path",
			description: "Target path is relative to working directory",
			files: map[string]string{
				"config.json": "file",
			},
			copyFrom:      ".",
			copyTo:        "relative/path/config.json",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"relative/path/config.json": "file",
			},
		},
		{
			name:        "absolute_target_path",
			description: "Target path is absolute",
			files: map[string]string{
				"data.txt": "file",
			},
			copyFrom:      ".",
			copyTo:        "/absolute/path/data.txt",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"absolute/path/data.txt": "file",
			},
		},
		{
			name:        "with_permissions_755",
			description: "File copied with 755 permissions",
			files: map[string]string{
				"script.sh": "file",
			},
			copyFrom:      ".",
			copyTo:        "/dest/script.sh",
			permissions:   "755",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/script.sh": "file",
			},
		},
		{
			name:        "with_permissions_644",
			description: "File copied with 644 permissions",
			files: map[string]string{
				"readme.md": "file",
			},
			copyFrom:      ".",
			copyTo:        "/dest/readme.md",
			permissions:   "644",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/readme.md": "file",
			},
		},
		{
			name:        "directory_with_permissions_recursive",
			description: "Directory contents get permissions applied recursively",
			files: map[string]string{
				"app/":          "dir",
				"app/file1.txt": "file",
				"app/file2.txt": "file",
			},
			copyFrom:      "app/",
			copyTo:        "/dest/",
			permissions:   "700",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/file1.txt": "file",
				"dest/file2.txt": "file",
			},
		},
		{
			name:             "empty_source_folder",
			description:      "Empty extraction should fail with error",
			files:            map[string]string{},
			copyFrom:         ".",
			copyTo:           "/dest/file.txt",
			shouldSucceed:    false,
			expectedExitCode: 1,
			expectedError:    "Error: sourceFolder is empty",
			expectedPaths:    map[string]string{},
		},
		{
			name:        "symlink_preservation",
			description: "Symlinks should be preserved as symlinks",
			files: map[string]string{
				"link.txt": "symlink",
			},
			copyFrom:      ".",
			copyTo:        "/dest/link.txt",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/link.txt": "symlink",
			},
		},
		{
			name:        "nested_folder_with_symlinks",
			description: "Nested folders containing symlinks",
			files: map[string]string{
				"app/":            "dir",
				"app/config.json": "file",
				"app/link":        "symlink",
				"app/data/":       "dir",
				"app/data/file":   "file",
				"app/data/link2":  "symlink",
			},
			copyFrom:      "app/",
			copyTo:        "/dest/",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/config.json": "file",
				"dest/link":        "symlink",
				"dest/data/":       "dir",
				"dest/data/file":   "file",
				"dest/data/link2":  "symlink",
			},
		},
		{
			name:        "hidden_files_and_directories",
			description: "Hidden files (.dotfiles) should be moved",
			files: map[string]string{
				"app/":                   "dir",
				"app/visible.txt":        "file",
				"app/.dotfile":           "file",
				"app/.hidden/":           "dir",
				"app/.hidden/nested.txt": "file",
			},
			copyFrom:      "app/",
			copyTo:        "/dest/",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"dest/visible.txt":        "file",
				"dest/.dotfile":           "file",
				"dest/.hidden/":           "dir",
				"dest/.hidden/nested.txt": "file",
			},
		},
		{
			name:        "deeply_nested_file",
			description: "Deeply nested file should be copied correctly",
			files: map[string]string{
				"app/l1/l2/l3/l4/l5/deep.txt": "file",
			},
			copyFrom:      "app/l1/l2/l3/l4/l5/deep.txt",
			copyTo:        ".",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"deep.txt": "file",
			},
		},
		{
			name:        "deeply_nested_folder",
			description: "Deeply nested folder should be copied correctly",
			files: map[string]string{
				"app/l1/l2/l3/l4/l5/deep.txt": "file",
			},
			copyFrom:      "app/l1/l2/l3/l4",
			copyTo:        ".",
			shouldSucceed: true,
			expectedPaths: map[string]string{
				"l5/deep.txt": "file",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sourceDir, targetBaseDir, workDir := setupTestEnvironment(t)

			// Internal: create the hash/unpack directory structure (mimics tar extraction)
			// This is hidden from the test case definition
			hashDir := filepath.Join(sourceDir, "hash_"+tc.name)
			unpackDir := filepath.Join(hashDir, "unpack")
			require.NoError(t, os.MkdirAll(unpackDir, 0o755))

			// Create files and directories
			createFilesAndDirs(t, unpackDir, tc.files)

			// Internal: construct SourcePath (sbxUnpackPath + user's copyFrom path)
			// This mimics how copy.go constructs the path: filepath.Join(sbxUnpackPath, sourcePath)
			sourcePath := filepath.Join(unpackDir, tc.copyFrom)

			// Make target path absolute if it starts with /
			targetPath := tc.copyTo
			if filepath.IsAbs(targetPath) {
				targetPath = filepath.Join(targetBaseDir, targetPath)
			}

			// Set owner if not specified
			owner := tc.owner
			if owner == "" {
				owner = currentUser
			}

			// Generate script
			data := copyScriptData{
				SourcePath:  sourcePath,
				TargetPath:  targetPath,
				Owner:       owner,
				Permissions: tc.permissions,
			}

			script := renderTemplate(t, data)

			// Execute script
			stdout, stderr, exitCode := executeScript(t, script, workDir)

			// Verify exit code
			if tc.shouldSucceed {
				assert.Equal(t, 0, exitCode, "Script should succeed. stdout: %s, stderr: %s", stdout, stderr)
			} else {
				if tc.expectedExitCode != 0 {
					assert.Equal(t, tc.expectedExitCode, exitCode, "Exit code should match")
				} else {
					assert.NotEqual(t, 0, exitCode, "Script should fail")
				}
			}

			// Verify error message if specified
			if tc.expectedError != "" {
				assert.Contains(t, stderr, tc.expectedError, "Error message should contain expected text")
			}

			// Verify expected paths exist
			if len(tc.expectedPaths) > 0 {
				baseDir := targetBaseDir
				// For relative paths, verify in workDir
				if !filepath.IsAbs(tc.copyTo) {
					baseDir = workDir
				}
				verifyFilesAndDirs(t, baseDir, tc.expectedPaths)
			}

			// Special verification for permissions tests
			if tc.permissions != "" && tc.shouldSucceed {
				baseDir := targetBaseDir
				if !filepath.IsAbs(tc.copyTo) {
					baseDir = workDir
				}
				for path, entryType := range tc.expectedPaths {
					if entryType == "file" {
						fullPath := filepath.Join(baseDir, path)
						perms := getFilePermissions(t, fullPath)
						expectedPerms := os.FileMode(0)
						fmt.Sscanf(tc.permissions, "%o", &expectedPerms)
						assert.Equal(t, expectedPerms, perms, "File %s should have %s permissions", path, tc.permissions)
					}
				}
			}
		})
	}
}
