package utils

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfrog/build-info-go/entities"
	"github.com/stretchr/testify/require"

	"github.com/jfrog/build-info-go/tests"
	"github.com/jfrog/build-info-go/utils"
	"github.com/stretchr/testify/assert"
)

var pnpmLogger = utils.NewDefaultLogger(utils.INFO)

func TestPnpmReadPackageInfo(t *testing.T) {
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(pnpmLogger)
	if err != nil {
		assert.NoError(t, err)
		return
	}

	testcases := []struct {
		json string
		pi   *PackageInfo
	}{
		{`{ "name": "build-info-go-tests", "version": "1.0.0", "description": "test package"}`,
			&PackageInfo{Name: "build-info-go-tests", Version: "1.0.0", Scope: ""}},
		{`{ "name": "@jfrog/build-info-go-tests", "version": "1.0.0", "description": "test package"}`,
			&PackageInfo{Name: "build-info-go-tests", Version: "1.0.0", Scope: "@jfrog"}},
		{`{}`, &PackageInfo{}},
	}
	for _, test := range testcases {
		t.Run(test.json, func(t *testing.T) {
			packInfo, err := ReadPackageInfo([]byte(test.json), pnpmVersion)
			assert.NoError(t, err)
			assert.Equal(t, test.pi, packInfo)
		})
	}
}

func TestPnpmReadPackageInfoFromPackageJsonIfExists(t *testing.T) {
	// Prepare tests data
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(pnpmLogger)
	assert.NoError(t, err)
	path, err := filepath.Abs(filepath.Join("..", "testdata"))
	assert.NoError(t, err)
	projectPath, cleanup := tests.CreatePnpmTest(t, path, "project1", pnpmVersion)
	defer cleanup()

	// Prepare test cases
	testCases := []struct {
		testName             string
		packageJsonDirectory string
		expectedPackageInfo  *PackageInfo
	}{
		{"Happy flow", projectPath, &PackageInfo{Name: "build-info-go-tests", Version: "1.0.0"}},
		{"No package.json in path", path, &PackageInfo{Name: "", Version: ""}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			// Read package info
			packageInfo, err := ReadPackageInfoFromPackageJsonIfExists(testCase.packageJsonDirectory, pnpmVersion)
			assert.NoError(t, err)

			// Remove "v" prefix, if exist
			removeVersionPrefixes(packageInfo)

			// Check results
			assert.Equal(t, testCase.expectedPackageInfo.Name, packageInfo.Name)
			assert.Equal(t, testCase.expectedPackageInfo.Version, strings.TrimPrefix(packageInfo.Version, "v"))
		})
	}
}

func TestPnpmReadPackageInfoFromPackageJsonIfExistErr(t *testing.T) {
	// Prepare test data
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(pnpmLogger)
	assert.NoError(t, err)
	tempDir, createTempDirCallback := tests.CreateTempDirWithCallbackAndAssert(t)
	assert.NoError(t, err)
	defer createTempDirCallback()

	// Create bad package.json file and expect error
	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "package.json"), []byte("non json file"), 0600))
	_, err = ReadPackageInfoFromPackageJsonIfExists(tempDir, pnpmVersion)
	assert.IsType(t, &json.SyntaxError{}, err)
}

func TestPnpmParseDependencies(t *testing.T) {
	dependenciesJsonList, err := os.ReadFile(filepath.Join("..", "testdata", "pnpm", "dependenciesList.json"))
	if err != nil {
		t.Error(err)
	}

	expectedDependenciesList := []struct {
		Key        string
		pathToRoot [][]string
	}{
		{"underscore:1.4.4", [][]string{{"binary-search-tree:0.2.4", "nedb:1.0.2", "root"}}},
		{"@jfrog/npm_scoped:1.0.0", [][]string{{"root"}}},
		{"xml:1.0.1", [][]string{{"root"}}},
		{"xpm:0.1.1", [][]string{{"@jfrog/npm_scoped:1.0.0", "root"}}},
		{"binary-search-tree:0.2.4", [][]string{{"nedb:1.0.2", "root"}}},
		{"nedb:1.0.2", [][]string{{"root"}}},
		{"@ilg/es6-promisifier:0.1.9", [][]string{{"@ilg/cli-start-options:0.1.19", "xpm:0.1.1", "@jfrog/npm_scoped:1.0.0", "root"}}},
		{"wscript-avoider:3.0.2", [][]string{{"@ilg/cli-start-options:0.1.19", "xpm:0.1.1", "@jfrog/npm_scoped:1.0.0", "root"}}},
		{"yaml:0.2.3", [][]string{{"root"}}},
		{"@ilg/cli-start-options:0.1.19", [][]string{{"xpm:0.1.1", "@jfrog/npm_scoped:1.0.0", "root"}}},
		{"async:0.2.10", [][]string{{"nedb:1.0.2", "root"}}},
		{"find:0.2.7", [][]string{{"root"}}},
		{"jquery:3.2.0", [][]string{{"root"}}},
		{"nub:1.0.0", [][]string{{"find:0.2.7", "root"}, {"root"}}},
		{"shopify-liquid:1.d7.9", [][]string{{"xpm:0.1.1", "@jfrog/npm_scoped:1.0.0", "root"}}},
	}
	dependencies := make(map[string]*pnpmDependencyInfo)
	err = parsePnpmDependencies(dependenciesJsonList, []string{"root"}, dependencies, pnpmLsDependencyParser, false, utils.NewDefaultLogger(utils.INFO))
	assert.NoError(t, err)
	assert.Equal(t, len(expectedDependenciesList), len(dependencies))
	for _, eDependency := range expectedDependenciesList {
		found := false
		for aDependency, v := range dependencies {
			if aDependency == eDependency.Key && assert.ElementsMatch(t, v.RequestedBy, eDependency.pathToRoot) {
				found = true
				break
			}
		}
		assert.True(t, found, "The expected dependency:", eDependency, "is missing from the actual dependencies list:\n", dependencies)
	}
}

func TestPnpmBundledDependenciesList(t *testing.T) {
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(pnpmLogger)
	assert.NoError(t, err)
	path, err := filepath.Abs(filepath.Join("..", "testdata"))
	assert.NoError(t, err)

	projectPath, cleanup := tests.CreatePnpmTest(t, path, "project1", pnpmVersion)
	defer cleanup()
	pnpmArgs := []string{}

	validatePnpmDependencies(t, projectPath, pnpmArgs)
}

// This test case verifies that CalculateDependenciesMap correctly handles the exclusion of 'node_modules'
// and updates 'package-lock.json' as required, based on the 'IgnoreNodeModules' and 'OverwritePackageLock' parameters.
func TestPnpmDependencyPackageLockOnly(t *testing.T) {
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(pnpmLogger)
	require.NoError(t, err)
	if !pnpmVersion.AtLeast("10.23") {
		t.Skip("Running on pnpm v10.23 and above only, skipping...")
	}
	path, cleanup := tests.CreateTestProject(t, filepath.Join("..", "testdata/pnpm/project6"))
	defer cleanup()
	assert.NoError(t, utils.MoveFile(filepath.Join(path, "pnpm-lock_test.yaml"), filepath.Join(path, "pnpm-lock.yaml")))
	// sleep so the package.json modified time will be bigger than the pnpm-lock.yaml, this make sure it will recalculate lock file.
	require.NoError(t, os.Chtimes(filepath.Join(path, "package.json"), time.Now(), time.Now().Add(time.Millisecond*20)))

	// Calculate dependencies.
	dependencies, err := CalculatePnpmDependenciesMap("pnpm", path, "jfrogtest",
		PnpmTreeDepListParam{Args: []string{}, IgnoreNodeModules: true, OverwritePnpmLock: true}, pnpmLogger, false)
	assert.NoError(t, err)
	var expectedRes = getPnpmExpectedRespForTestDependencyPackageLockOnly()
	assert.Equal(t, expectedRes, dependencies)
}

func TestCalculatePnpmDependenciesMapWithProhibitedInstallation(t *testing.T) {
	path, cleanup := tests.CreateTestProject(t, filepath.Join("..", "testdata", "pnpm", "noBuildProject"))
	defer cleanup()

	dependencies, err := CalculatePnpmDependenciesMap("pnpm", path, "jfrogtest",
		PnpmTreeDepListParam{Args: []string{}, IgnoreNodeModules: false, OverwritePnpmLock: false}, pnpmLogger, true)

	assert.Nil(t, dependencies)
	assert.Error(t, err)
	var notInstalledError *utils.ErrProjectNotInstalled
	assert.True(t, errors.As(err, &notInstalledError))
}

func getPnpmExpectedRespForTestDependencyPackageLockOnly() map[string]*pnpmDependencyInfo {
	return map[string]*pnpmDependencyInfo{
		"underscore:1.13.6": {
			Dependency: entities.Dependency{
				Id:          "underscore:1.13.6",
				Scopes:      []string{"prod"},
				RequestedBy: [][]string{{"jfrogtest"}},
				Checksum:    entities.Checksum{},
			},
			pnpmLsDependency: &pnpmLsDependency{
				Name:      "underscore",
				Version:   "1.13.6",
				Resolved:  "https://registry.npmjs.org/underscore/-/underscore-1.13.6.tgz",
			},
		},
		"cors.js:0.0.1-security": {
			Dependency: entities.Dependency{
				Id:          "cors.js:0.0.1-security",
				Scopes:      []string{"prod"},
				RequestedBy: [][]string{{"jfrogtest"}},
				Checksum:    entities.Checksum{},
			},
			pnpmLsDependency: &pnpmLsDependency{
				Name:      "cors.js",
				Version:   "0.0.1-security",
				Resolved:  "https://registry.npmjs.org/cors.js/-/cors.js-0.0.1-security.tgz",
			},
		},
		"lightweight:0.1.0": {
			Dependency: entities.Dependency{
				Id:          "lightweight:0.1.0",
				Scopes:      []string{"prod"},
				RequestedBy: [][]string{{"jfrogtest"}},
				Checksum:    entities.Checksum{},
			},
			pnpmLsDependency: &pnpmLsDependency{
				Name:      "lightweight",
				Version:   "0.1.0",
				Resolved:  "https://registry.npmjs.org/lightweight/-/lightweight-0.1.0.tgz",
			},
		},
		"minimist:0.1.0": {
			Dependency: entities.Dependency{
				Id:          "minimist:0.1.0",
				Scopes:      []string{"prod"},
				RequestedBy: [][]string{{"jfrogtest"}},
				Checksum:    entities.Checksum{},
			},
			pnpmLsDependency: &pnpmLsDependency{
				Name:      "minimist",
				Version:   "0.1.0",
				Resolved:  "https://registry.npmjs.org/minimist/-/minimist-0.1.0.tgz",
			},
		},
	}
}

// A project built differently for each operating system.
func TestPnpmDependenciesTreeDifferentBetweenOKs(t *testing.T) {
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(pnpmLogger)
	assert.NoError(t, err)
	path, err := filepath.Abs(filepath.Join("..", "testdata"))
	assert.NoError(t, err)
	projectPath, cleanup := tests.CreatePnpmTest(t, path, "project4", pnpmVersion)
	defer cleanup()
	cachePath := filepath.Join(projectPath, "tmpcache")

	// Set the store-dir in .npmrc
	err = SetPnpmConfigCache(projectPath, cachePath, pnpmLogger)
	assert.NoError(t, err)

	// Install all the project's dependencies.
	pnpmInstallArgs := []string{"--frozen-lockfile"}
	pnpmListArgs := []string{}
	_, _, err = RunPnpmCmd("pnpm", projectPath, AppendPnpmCommand(pnpmInstallArgs, pnpmInstallCommand), pnpmLogger)
	assert.NoError(t, err)

	// Calculate dependencies.
	dependencies, err := CalculatePnpmDependenciesList("pnpm", projectPath, "bundle-dependencies", PnpmTreeDepListParam{Args: pnpmListArgs}, pnpmLogger)
	assert.NoError(t, err)

	assert.Greater(t, len(dependencies), 0, "Error: dependencies are not found!")

	// Remove node_modules directory, then calculate dependencies by package-lock.
	assert.NoError(t, utils.RemoveTempDir(filepath.Join(projectPath, "node_modules")))

	// NOTE: unlike npm, if pnpm ls is called without node_modules, it will not show any dependencies.
	// i.e. it can only list MET dependencies, so after deleting node_modules, the expected value for
	// len(dependencies) is 0
	dependencies, err = CalculatePnpmDependenciesList("pnpm", projectPath, "build-info-go-tests", PnpmTreeDepListParam{Args: pnpmListArgs}, pnpmLogger)
	assert.NoError(t, err)

	// Asserting there is at least one dependency.
	assert.Equal(t, len(dependencies), 0, "Error: pnpm should not list dependencies after removing node_modules!")
}

func TestPnpmProdFlag(t *testing.T) {
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(pnpmLogger)
	assert.NoError(t, err)
	path, err := filepath.Abs(filepath.Join("..", "testdata"))
	assert.NoError(t, err)
	testDependencyScopes := []struct {
		scope     string
		totalDeps int
	}{
		{"", 2},
		{"--prod", 1},
	}
	for _, entry := range testDependencyScopes {
		func() {
			projectPath, cleanup := tests.CreatePnpmTest(t, path, "project3", pnpmVersion)
			defer cleanup()
			cachePath := filepath.Join(projectPath, "tmpcache")

			// Set the store-dir in .npmrc
			err = SetPnpmConfigCache(projectPath, cachePath, pnpmLogger)
			assert.NoError(t, err)

			pnpmInstallArgs := []string{"--frozen-lockfile", entry.scope}
			pnpmListArgs := []string{}

			// Install dependencies in the pnpm project.
			_, _, err = RunPnpmCmd("pnpm", projectPath, AppendPnpmCommand(pnpmInstallArgs, pnpmInstallCommand), pnpmLogger)
			assert.NoError(t, err)

			// Calculate dependencies with scope.
			dependencies, err := CalculatePnpmDependenciesList("pnpm", projectPath, "build-info-go-tests", PnpmTreeDepListParam{Args: pnpmListArgs}, pnpmLogger)
			assert.NoError(t, err)
			assert.Len(t, dependencies, entry.totalDeps)
		}()
	}
}

func TestGetConfigCachePnpmIntegration(t *testing.T) {
	innerLogger := utils.NewDefaultLogger(utils.DEBUG)
	pnpmVersion, _, err := GetPnpmVersionAndExecPath(innerLogger)
	assert.NoError(t, err)

	// Create the first pnpm project which contains peerDependencies, devDependencies & bundledDependencies
	path, err := filepath.Abs(filepath.Join("..", "testdata"))
	assert.NoError(t, err)
	projectPath, cleanup := tests.CreatePnpmTest(t, path, "project1", pnpmVersion)
	defer cleanup()
	cachePath := filepath.Join(projectPath, "tmpcache")

	// Set the store-dir in .npmrc
	err = SetPnpmConfigCache(projectPath, cachePath, innerLogger)
	assert.NoError(t, err)

	pnpmInstallArgs := []string{"--frozen-lockfile"}
	pnpmArgs := []string{}

	// Install dependencies in the pnpm project.
	_, _, err = RunPnpmCmd("pnpm", projectPath, AppendPnpmCommand(pnpmInstallArgs, pnpmInstallCommand), innerLogger)
	assert.NoError(t, err)

	configCache, err := GetPnpmConfigCache(projectPath, "pnpm", pnpmArgs, innerLogger)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(cachePath, "v3", "files"), configCache)

	oldCache := os.Getenv("pnpm_config_cache")
	if oldCache != "" {
		defer func() {
			assert.NoError(t, os.Setenv("pnpm_config_cache", oldCache))
		}()
	}
	assert.NoError(t, os.Setenv("pnpm_config_cache", cachePath))
	configCache, err = GetPnpmConfigCache(projectPath, "pnpm", []string{}, innerLogger)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(cachePath, "v3", "files"), configCache)
}

// This function executes a CI install using --frozen-lockfile, then validate generating dependencies in two possible scenarios:
// 1. node_module exists in the project.
// 2. node_module doesn't exist in the project and generating dependencies needs package-lock.
func validatePnpmDependencies(t *testing.T, projectPath string, pnpmListArgs []string) {
	// Install dependencies in the pnpm project.
	pnpmInstallArgs := AppendPnpmCommand(pnpmListArgs, "--frozen-lockfile")
	_, _, err := RunPnpmCmd("pnpm", projectPath, AppendPnpmCommand(pnpmInstallArgs, pnpmInstallCommand), pnpmLogger)
	assert.NoError(t, err)

	// Calculate dependencies.
	dependencies, err := CalculatePnpmDependenciesList("pnpm", projectPath, "build-info-go-tests", PnpmTreeDepListParam{Args: pnpmListArgs}, pnpmLogger)
	assert.NoError(t, err)

	assert.Greater(t, len(dependencies), 0, "Error: dependencies are not found!")

	// Remove node_modules directory, then calculate dependencies by package-lock.
	assert.NoError(t, utils.RemoveTempDir(filepath.Join(projectPath, "node_modules")))

	// NOTE: unlike npm, if pnpm ls is called without node_modules, it will not show any dependencies.
	// i.e. it can only list MET dependencies, so after deleting node_modules, the expected value for
	// len(dependencies) is 0
	dependencies, err = CalculatePnpmDependenciesList("pnpm", projectPath, "build-info-go-tests", PnpmTreeDepListParam{Args: pnpmListArgs}, pnpmLogger)
	assert.NoError(t, err)

	// Asserting there is at least one dependency.
	assert.Equal(t, len(dependencies), 0, "Error: pnpm should not list dependencies after removing node_modules!")
}

func TestParsePnpmDependenciesEdgeCases(t *testing.T) {
	testcases := []struct {
		name                string
		isDev               bool
		inputJson           string
		expectedId          string
		shouldBeSkipped     bool
		expectParseError    bool
		expectedRequestedBy [][]string
	}{
		{
			name:             "Git URL with hash in resolved",
			inputJson:        `{"@angular/dev-infra-private":{"resolved": "git+ssh://git@github.com/angular/dev-infra-private-builds.git#e4a13cfd135ec766dc9148ba4fe4d3ac76d94137"}}`,
			expectedId:       "@angular/dev-infra-private:e4a13cfd135ec766dc9148ba4fe4d3ac76d94137",
			shouldBeSkipped:  false,
			expectParseError: false,
		},
		{
			name:      "Git URL without hash in resolved",
			inputJson: `{"my-pkg":{"resolved": "git+https://github.com/user/repo.git"}}`,
			expectedId: func() string {
				return "my-pkg:"
			}(),
			shouldBeSkipped:  false,
			expectParseError: false,
		},
		{
			name:      "Local file path in resolved",
			inputJson: `{"my-local-pkg":{"resolved": "file:../shared/my-local-pkg"}}`,
			expectedId: func() string {
				return "my-local-pkg:"
			}(),
			shouldBeSkipped:  false,
			expectParseError: false,
		},
		{
			name:      "Direct tarball URL in resolved",
			inputJson: `{"my-tarball-pkg":{"resolved": "https://example.com/pkg-1.0.0.tgz"}}`,
			expectedId: func() string {
				return "my-tarball-pkg:"
			}(),
			shouldBeSkipped:  false,
			expectParseError: false,
		},
		{
			name:             "No version and no resolved, but missing",
			inputJson:        `{"bad-pkg":{"missing": true}}`,
			shouldBeSkipped:  true,
			expectParseError: false,
		},
		{
			name:             "No version and no resolved, not missing",
			isDev:            true,
			inputJson:        `{"bad-pkg":{"dev": true}}`,
			shouldBeSkipped:  false,
			expectParseError: true,
		},
		{
			name:             "Missing peer dependency",
			inputJson:        `{"peer-pkg":{"missing": true}}`,
			shouldBeSkipped:  true,
			expectParseError: false,
		},
		{
			name:             "Regular dependency is not affected",
			inputJson:        `{"react":{"version": "18.2.0", "integrity": "sha512-..."}}`,
			expectedId:       "react:18.2.0",
			shouldBeSkipped:  false,
			expectParseError: false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			depsMap := make(map[string]*pnpmDependencyInfo)
			parseFunc := pnpmLsDependencyParser
			err := parsePnpmDependencies([]byte(tc.inputJson), []string{"root"}, depsMap, parseFunc, tc.isDev == true, &utils.NullLog{})

			if tc.expectParseError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			if tc.shouldBeSkipped {
				assert.Empty(t, depsMap, "Expected dependency to be skipped, but it was added")
			} else {
				assert.Len(t, depsMap, 1, "Expected exactly one dependency")
				// Check if the key exists
				depInfo, ok := depsMap[tc.expectedId]
				assert.True(t, ok, "Expected dependency ID '%s' not found in map", tc.expectedId)
				if ok {
					assert.Equal(t, tc.expectedId, depInfo.Id, "Dependency ID mismatch")
				}
			}
		})
	}
}
