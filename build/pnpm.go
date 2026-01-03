package build

import (
	"errors"
	"os"
	"strings"

	buildutils "github.com/jfrog/build-info-go/build/utils"
	"github.com/jfrog/build-info-go/entities"
	"github.com/jfrog/build-info-go/utils"
)

const minSupportedPnpmVersion = "8.15.0"

type PnpmModule struct {
	containingBuild  *Build
	name             string
	srcPath          string
	executablePath   string
	pnpmInstallArgs  []string
	pnpmListArgs     []string
	collectBuildInfo bool
}

// Pass an empty string for srcPath to find the pnpm project in the working directory.
func newPnpmModule(srcPath string, containingBuild *Build) (*PnpmModule, error) {
	pnpmVersion, executablePath, err := buildutils.GetPnpmVersionAndExecPath(containingBuild.logger)
	if err != nil {
		return nil, err
	}
	if pnpmVersion.Compare(minSupportedPnpmVersion) > 0 {
		return nil, errors.New("pnpm CLI must have version " + minSupportedPnpmVersion + " or higher. The current version is: " + pnpmVersion.GetVersion())
	}

	if srcPath == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		srcPath, err = utils.FindFileInDirAndParents(wd, "package.json")
		if err != nil {
			return nil, err
		}
	}

	// Read module name
	packageInfo, err := buildutils.ReadPackageInfoFromPackageJsonIfExists(srcPath, pnpmVersion)
	if err != nil {
		return nil, err
	}
	name := packageInfo.BuildInfoModuleId()

	return &PnpmModule{name: name, srcPath: srcPath, containingBuild: containingBuild, executablePath: executablePath}, nil
}

func (nm *PnpmModule) Build() error {
	if len(nm.pnpmInstallArgs) > 0 && len(nm.pnpmListArgs) > 0 {
		output, _, err := buildutils.RunPnpmCmd(nm.executablePath, nm.srcPath, nm.pnpmInstallArgs, &utils.NullLog{})
		if len(output) > 0 {
			nm.containingBuild.logger.Output(strings.TrimSpace(string(output)))
		}
		if err != nil {
			return err
		}
		// After executing the user-provided command, cleaning pnpmInstallArgs is needed.
		nm.filterPnpmArgsFlags()
	}
	if !nm.collectBuildInfo {
		return nil
	}
	return nm.CalcDependencies()
}

func (nm *PnpmModule) CalcDependencies() error {
	if !nm.containingBuild.buildNameAndNumberProvided() {
		return errors.New("a build name must be provided in order to collect the project's dependencies")
	}
	buildInfoDependencies, err := buildutils.CalculatePnpmDependenciesList(nm.executablePath, nm.srcPath, nm.name,
		buildutils.PnpmTreeDepListParam{Args: nm.pnpmListArgs, InstallCommandArgs: nm.pnpmInstallArgs}, nm.containingBuild.logger)
	if err != nil {
		return err
	}
	buildInfoModule := entities.Module{Id: nm.name, Type: entities.Pnpm, Dependencies: buildInfoDependencies}
	buildInfo := &entities.BuildInfo{Modules: []entities.Module{buildInfoModule}}
	return nm.containingBuild.SaveBuildInfo(buildInfo)
}

func (nm *PnpmModule) SetName(name string) {
	nm.name = name
}

func (nm *PnpmModule) SetPnpmArgs(pnpmListArgs []string, pnpmInstallArgs []string) {
	nm.pnpmListArgs = pnpmListArgs
	nm.pnpmInstallArgs = pnpmInstallArgs
}

func (nm *PnpmModule) SetCollectBuildInfo(collectBuildInfo bool) {
	nm.collectBuildInfo = collectBuildInfo
}

func (nm *PnpmModule) AddArtifacts(artifacts ...entities.Artifact) error {
	return nm.containingBuild.AddArtifacts(nm.name, entities.Pnpm, artifacts...)
}

// This function discards the pnpm command in pnpmInstallArgs and pnpmListArgs and keeps only the command flags.
// It is necessary for the pnpm command's name to come before the pnpm command's flags in pnpmInstallArgs for the function to work correctly.
func (nm *PnpmModule) filterPnpmArgsFlags() {
	if len(nm.pnpmInstallArgs) == 1 && !strings.HasPrefix(nm.pnpmInstallArgs[0], "-") {
		nm.pnpmInstallArgs = []string{}
	}
	for argIndex := 0; argIndex < len(nm.pnpmInstallArgs); argIndex++ {
		if strings.HasPrefix(nm.pnpmInstallArgs[argIndex], "-") {
			nm.pnpmInstallArgs = nm.pnpmInstallArgs[argIndex:]
		}
	}

	if len(nm.pnpmListArgs) == 1 && !strings.HasPrefix(nm.pnpmListArgs[0], "-") {
		nm.pnpmListArgs = []string{}
	}
	for argIndex := 0; argIndex < len(nm.pnpmListArgs); argIndex++ {
		if strings.HasPrefix(nm.pnpmListArgs[argIndex], "-") {
			nm.pnpmListArgs = nm.pnpmListArgs[argIndex:]
		}
	}
}
