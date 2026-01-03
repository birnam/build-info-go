package build

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	buildutils "github.com/jfrog/build-info-go/build/utils"
	"github.com/jfrog/build-info-go/entities"
	"github.com/jfrog/build-info-go/tests"
	"github.com/jfrog/build-info-go/utils"
	"github.com/stretchr/testify/assert"
)

var pnpmLogger = utils.NewDefaultLogger(utils.INFO)

func TestGenerateBuildInfoForPnpm(t *testing.T) {
	service := NewBuildInfoService()
	pnpmBuild, err := service.GetOrCreateBuild("build-info-go-test-pnpm", strconv.FormatInt(time.Now().Unix(), 10))
	assert.NoError(t, err)
	defer func() {
		// assert.NoError(t, pnpmBuild.Clean())
	}()
	pnpmVersion, _, err := buildutils.GetPnpmVersionAndExecPath(pnpmLogger)
	assert.NoError(t, err)

	// Create pnpm project.
	path, err := filepath.Abs(filepath.Join(".", "testdata"))
	assert.NoError(t, err)
	// projectPath, cleanup := tests.CreatePnpmTest(t, path, "project3", pnpmVersion)
	projectPath, _ := tests.CreatePnpmTest(t, path, "project3", pnpmVersion)
	// defer cleanup()

	// Set the store-dir in .npmrc
	cachePath := filepath.Join(projectPath, "tmpcache")
	err = buildutils.SetPnpmConfigCache(projectPath, cachePath, pnpmLogger)
	assert.NoError(t, err)

	// Install dependencies in the pnpm project.
	pnpmInstallArgs := []string{}
	pnpmListArgs := []string{}
	_, _, err = buildutils.RunPnpmCmd("pnpm", projectPath, buildutils.AppendPnpmCommand(pnpmInstallArgs, "install"), pnpmLogger)
	assert.NoError(t, err)
	pnpmModule, err := pnpmBuild.AddPnpmModule(projectPath)
	assert.NoError(t, err)
	pnpmModule.SetPnpmArgs(pnpmListArgs, pnpmInstallArgs)
	err = pnpmModule.CalcDependencies()
	assert.NoError(t, err)
	buildInfo, err := pnpmBuild.ToBuildInfo()
	assert.NoError(t, err)

	// Verify results.
	expectedBuildInfoJson := filepath.Join(projectPath, "expected_pnpm_buildinfo.json")
	expectedBuildInfo := tests.GetBuildInfo(t, expectedBuildInfoJson)
	match, err := entities.IsEqualModuleSlices(buildInfo.Modules, expectedBuildInfo.Modules)
	assert.NoError(t, err)
	if !match {
		tests.PrintBuildInfoMismatch(t, expectedBuildInfo.Modules, buildInfo.Modules)
	}
}

func TestFilterPnpmArgsFlags(t *testing.T) {
	service := NewBuildInfoService()
	pnpmBuild, err := service.GetOrCreateBuild("build-info-go-test-pnpm", strconv.FormatInt(time.Now().Unix(), 10))
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, pnpmBuild.Clean())
	}()
	pnpmVersion, _, err := buildutils.GetPnpmVersionAndExecPath(pnpmLogger)
	assert.NoError(t, err)

	// Create pnpm project.
	path, err := filepath.Abs(filepath.Join(".", "testdata"))
	assert.NoError(t, err)
	projectPath, cleanup := tests.CreatePnpmTest(t, path, "project3", pnpmVersion)
	defer cleanup()

	// Set arguments in pnpmListArgs and pnpmInstallArgs.
	pnpmListArgs := []string{"ls"}
	pnpmInstallArgs := []string{}
	_, _, err = buildutils.RunPnpmCmd("pnpm", projectPath, buildutils.AppendPnpmCommand(pnpmInstallArgs, "install"), pnpmLogger)
	assert.NoError(t, err)
	pnpmModule, err := pnpmBuild.AddPnpmModule(projectPath)
	assert.NoError(t, err)
	pnpmModule.SetPnpmArgs(pnpmListArgs, pnpmInstallArgs)
	pnpmModule.filterPnpmArgsFlags()
	pnpmListArgs = []string{"config", "store-dir", "--json", "--all"}
	pnpmModule.SetPnpmArgs(pnpmListArgs, pnpmInstallArgs)
	pnpmModule.filterPnpmArgsFlags()
	expected := []string{"--json", "--all"}
	assert.Equal(t, expected, pnpmModule.pnpmListArgs)
}
