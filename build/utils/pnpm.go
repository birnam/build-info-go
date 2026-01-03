package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/jfrog/build-info-go/entities"
	"github.com/jfrog/build-info-go/utils"
	"github.com/jfrog/gofrog/version"
)

const pnpmInstallCommand = "install"

// CalculatePnpmDependenciesList gets an pnpm project's dependencies.
// bool to calculate checksums is omitted because pnpm only uses SHA512 (incompatible with build-info struct)
func CalculatePnpmDependenciesList(executablePath, srcPath, moduleId string, pnpmParams PnpmTreeDepListParam, log utils.Log) ([]entities.Dependency, error) {
	if log == nil {
		log = &utils.NullLog{}
	}

	// Calculate pnpm dependency tree using 'pnpm ls...'.
	dependenciesMap, err := CalculatePnpmDependenciesMap(executablePath, srcPath, moduleId, pnpmParams, log, false)
	if err != nil {
		return nil, err
	}
	var dependenciesList []entities.Dependency

	// NOTE: cannot scan ls output for integrity as is done with npm, that info is not provided by pnpm
	for _, dep := range dependenciesMap {
		dependenciesList = append(dependenciesList, dep.Dependency)
	}
	return dependenciesList, nil
}

type pnpmDependencyInfo struct {
	entities.Dependency
	*pnpmLsDependency
}

// Run 'pnpm list ...' command and parse the returned result to create a dependencies map of.
// The dependencies map looks like name:version -> entities.Dependency.
func CalculatePnpmDependenciesMap(executablePath, srcPath, moduleId string, pnpmParams PnpmTreeDepListParam, log utils.Log, skipInstall bool) (map[string]*pnpmDependencyInfo, error) {
	pnpmDependenciesMap := make(map[string]*pnpmDependencyInfo)
	// These arguments must be added at the end of the command, to override their other values (if existed in nm.pnpmInstallArgs).
	pnpmVersion, err := GetPnpmVersion(executablePath, log)
	if err != nil {
		return nil, err
	}
	nodeModulesExist, err := utils.IsDirExists(filepath.Join(srcPath, "node_modules"), false)
	if err != nil {
		return nil, err
	}
	var data []byte
	// When `skipInstall` is true, we aim to rely on the dependencies specified in the pnpm-lock.yaml, so Frogbot will not execute 'pnpm ls' on modules that are unbuilt and lack lock files (which may still have incomplete node_modules that could cause errors).
	if nodeModulesExist && !pnpmParams.IgnoreNodeModules && !skipInstall {
		data = runPnpmLsWithNodeModules(executablePath, srcPath, pnpmParams.Args, log)
	} else {
		// If we don't have node_modules, the function will use the pnpm-lock dependencies.
		data, err = runPnpmLsWithoutNodeModules(executablePath, srcPath, pnpmParams, log, pnpmVersion, skipInstall)
		if err != nil {
			return nil, err
		}
	}

	// Handle both array and object formats from pnpm ls
	// pnpm ls should return [{...}] but we will accommodate {...} as well
	var objectData []byte
	if firstElement, dataType, _, err := jsonparser.Get(data, "[0]"); err == nil && dataType == jsonparser.Object {
		// Data is an array, use first element
		objectData = firstElement
	} else {
		// Data is already an object or array access failed, use as-is
		objectData = data
	}

	// Parse the dependencies json object.
	return pnpmDependenciesMap, jsonparser.ObjectEach(objectData, func(key []byte, value []byte, dataType jsonparser.ValueType, offset int) (err error) {
		if string(key) == "dependencies" {
			err = parsePnpmDependencies(value, []string{moduleId}, pnpmDependenciesMap, pnpmLsDependencyParser, false, log)
		}
		if string(key) == "devDependencies" {
			err = parsePnpmDependencies(value, []string{moduleId}, pnpmDependenciesMap, pnpmLsDependencyParser, true, log)
		}
		return err
	})
}

func runPnpmLsWithNodeModules(executablePath, srcPath string, pnpmListArgs []string, log utils.Log) (data []byte) {
	pnpmListArgs = append(pnpmListArgs, "--json", "--long")
	data, errData, err := RunPnpmCmd(executablePath, srcPath, AppendPnpmCommand(pnpmListArgs, "ls"), log)
	if err != nil {
		// It is optional for the function to return this error.
		log.Warn(err.Error())
	} else if len(errData) > 0 {
		log.Warn("Encountered some issues while running 'pnpm ls' command:\n" + strings.TrimSpace(string(errData)))
	}
	return
}

func runPnpmLsWithoutNodeModules(executablePath, srcPath string, pnpmParams PnpmTreeDepListParam, log utils.Log, pnpmVersion *version.Version, skipInstall bool) ([]byte, error) {
	installRequired, err := isPnpmInstallRequired(srcPath, pnpmParams, pnpmVersion, log, skipInstall)
	if err != nil {
		return nil, err
	}

	if installRequired {
		err = installPnpmLock(executablePath, srcPath, pnpmParams.InstallCommandArgs, log, pnpmVersion)
		if err != nil {
			return nil, err
		}
	}
	// --lockfile-only was only added in version 10.23
	pnpmParams.Args = append(pnpmParams.Args, "--json", "--long")
	data, errData, err := RunPnpmCmd(executablePath, srcPath, AppendPnpmCommand(pnpmParams.Args, "ls"), log)
	if err != nil {
		log.Warn(err.Error())
	} else if len(errData) > 0 {
		log.Warn("Encountered some issues while running 'pnpm ls' command:\n" + strings.TrimSpace(string(errData)))
	}
	return data, nil
}

// This function determines whether a project installation is required by evaluating the following criteria:
// 1) Checks if the "pnpm-lock.yaml" file exists in the project directory.
// 2) Verifies if an installation command was provided by the user.
// 3) Checks if the lock file should be updated due to an override request.
//
// Conditions for triggering installation:
// - If the user provided an installation command, installation is required.
// - If the "pnpm-lock.yaml" file is missing or an override request to update the lock file exists, installation is required, unless the user explicitly requested to skip the installation.
//
// If installation is required but skipped by the user's request, an error is returned.
func isPnpmInstallRequired(srcPath string, pnpmParams PnpmTreeDepListParam, pnpmVersion *version.Version, log utils.Log, skipInstall bool) (bool, error) {
	isPnpmLockExist, err := utils.IsFileExists(filepath.Join(srcPath, "pnpm-lock.yaml"), false)
	if err != nil {
		return false, err
	}

	if !pnpmVersion.AtLeast("10.23") {
		return true, nil
	}

	if len(pnpmParams.InstallCommandArgs) > 0 {
		return true, nil
	}
	if !isPnpmLockExist || (pnpmParams.OverwritePnpmLock && checkIfPnpmLockFileShouldBeUpdated(srcPath, log)) {
		if skipInstall {
			return false, &utils.ErrProjectNotInstalled{UninstalledDir: srcPath}
		}
		return true, nil
	}
	return false, nil
}

func installPnpmLock(executablePath, srcPath string, pnpmInstallArgs []string, log utils.Log, pnpmVersion *version.Version) error {
	if pnpmVersion.AtLeast("10.23") {
		pnpmInstallArgs = append(pnpmInstallArgs, "--lockfile-only")
		// Installing pnpm-lock to generate the dependencies map.
		_, _, err := RunPnpmCmd(executablePath, srcPath, AppendPnpmCommand(pnpmInstallArgs, pnpmInstallCommand), log)
		if err != nil {
			return err
		}
		return nil
	}

	// full install required for versions < 10.23
	pnpmInstallArgs = append(pnpmInstallArgs, "--frozen-lockfile")
	_, _, err := RunPnpmCmd(executablePath, srcPath, AppendPnpmCommand(pnpmInstallArgs, pnpmInstallCommand), log)
	return err
}

// Check if package.json has been modified.
// This might indicate the addition of new packages to package.json that haven't been reflected in pnpm-lock.yaml.
func checkIfPnpmLockFileShouldBeUpdated(srcPath string, log utils.Log) bool {
	packageJsonInfo, err := os.Stat(filepath.Join(srcPath, "package.json"))
	if err != nil {
		log.Warn("Failed to get file info for package.json, err: %v", err)
		return false
	}

	packageJsonInfoModTime := packageJsonInfo.ModTime()
	pnpmLockInfo, err := os.Stat(filepath.Join(srcPath, "pnpm-lock.yaml"))
	if err != nil {
		log.Warn("Failed to get file info for pnpm-lock.yaml, err: %v", err)
		return false
	}
	pnpmLockInfoModTime := pnpmLockInfo.ModTime()
	return packageJsonInfoModTime.After(pnpmLockInfoModTime)
}

func GetPnpmVersion(executablePath string, log utils.Log) (*version.Version, error) {
	versionData, _, err := RunPnpmCmd(executablePath, "", []string{"--version"}, log)
	if err != nil {
		return nil, err
	}
	return version.NewVersion(string(versionData)), nil
}

type PnpmTreeDepListParam struct {
	// Required for the 'install' and 'ls' commands that could be triggered during the construction of the PNPM dependency tree
	Args []string
	// Optional user-supplied arguments for the 'install' command. These arguments are not available from all entry points. They may be employed when constructing the PNPM dependency tree, which could necessitate the execution of 'pnpm install...'
	InstallCommandArgs []string
	// Ignore the node_modules folder if exists, using the '--lockfile-only' flag
	IgnoreNodeModules bool
	// Rewrite pnpm-lock.yaml, if exists.
	OverwritePnpmLock bool
}

// pnpm >=8 ls results for a single dependency
type pnpmLsDependency struct {
	Name      string
	Version   string
	Resolved  string
	Integrity string
	InBundle  bool
	Dev       bool
	Optional  bool
	Missing bool
	Problems []string
	PeerMissing interface{}
}

// Return name:version of a dependency
func (nld *pnpmLsDependency) id() string {
	return nld.Name + ":" + nld.Version
}

func (nld *pnpmLsDependency) getScopes() (scopes []string) {
	if nld.Dev {
		scopes = append(scopes, "dev")
	} else {
		scopes = append(scopes, "prod")
	}
	if strings.HasPrefix(nld.Name, "@") {
		splitValues := strings.Split(nld.Name, "/")
		if len(splitValues) > 2 {
			scopes = append(scopes, splitValues[0])
		}
	}
	return
}

// Parses pnpm dependencies recursively and adds the collected dependencies to the given dependencies map.
func parsePnpmDependencies(data []byte, pathToRoot []string, dependencies map[string]*pnpmDependencyInfo, parseFunc func(data []byte) (*pnpmLsDependency, error), isDev bool, log utils.Log) error {
	return jsonparser.ObjectEach(data, func(key []byte, value []byte, dataType jsonparser.ValueType, offset int) error {
		if string(value) == "{}" {
			// Skip missing optional dependency.
			log.Debug(fmt.Sprintf("%s is missing. This may be the result of an optional dependency.", key))
			return nil
		}
		pnpmLsDependency, err := parseFunc(value)
		if err != nil {
			return err
		}
		// The dependency name is a key in the object, which is not always available inside the value.
		pnpmLsDependency.Name = string(key)

		if pnpmLsDependency.Version == "" {
			// Check if this is a non-registry dependency with a resolved field (e.g. git, file etc.)
			resolvedUrl := pnpmLsDependency.Resolved
			// If there's no resolved field, try to extract it from the problems array
			if resolvedUrl == "" && pnpmLsDependency.Problems != nil && len(pnpmLsDependency.Problems) > 0 {
				resolvedUrl = extractUrlFromProblems(pnpmLsDependency.Problems, pnpmLsDependency.Name)
			}
			switch {
			case resolvedUrl != "":
				version := extractVersionFromGitUrl(resolvedUrl);
				if version != "" {
					pnpmLsDependency.Version = version
				}
			case pnpmLsDependency.Missing || pnpmLsDependency.Problems != nil:
				// Skip missing peer dependency.
				log.Debug(fmt.Sprintf("%s is missing, this may be the result of an peer dependency.", key))
				return nil
			default:
				return errors.New("failed to parse '" + string(value) + "' from pnpm ls output.")
			}
		}
		appendPnpmDependency(dependencies, pnpmLsDependency, isDev, pathToRoot)
		transitive, _, _, err := jsonparser.Get(value, "dependencies")
		if err != nil && err.Error() != "Key path not found" {
			return err
		}
		if len(transitive) > 0 {
			if err := parsePnpmDependencies(transitive, append([]string{pnpmLsDependency.id()}, pathToRoot...), dependencies, parseFunc, isDev, log); err != nil {
				return err
			}
		}
		return nil
	})
}

func pnpmLsDependencyParser(data []byte) (*pnpmLsDependency, error) {
	pnpmLsDependency := new(pnpmLsDependency)
	return pnpmLsDependency, json.Unmarshal(data, &pnpmLsDependency)
}

func appendPnpmDependency(dependencies map[string]*pnpmDependencyInfo, dep *pnpmLsDependency, isDev bool, pathToRoot []string) {
	depId := dep.id()
	dep.Dev = isDev
	scopes := dep.getScopes()
	if dependencies[depId] == nil {
		dependency := &pnpmDependencyInfo{
			Dependency:      entities.Dependency{Id: depId},
			pnpmLsDependency: dep,
		}

		dependencies[depId] = dependency
	}
	if dependencies[depId].Integrity == "" {
		dependencies[depId].Integrity = dep.Integrity
	}
	dependencies[depId].Scopes = appendScopes(dependencies[depId].Scopes, scopes)
	dependencies[depId].RequestedBy = append(dependencies[depId].RequestedBy, pathToRoot)
}

func RunPnpmCmd(executablePath, srcPath string, pnpmInstallArgs []string, log utils.Log) (stdResult, errResult []byte, err error) {
	args := make([]string, 0)
	for i := 0; i < len(pnpmInstallArgs); i++ {
		if strings.TrimSpace(pnpmInstallArgs[i]) != "" {
			args = append(args, pnpmInstallArgs[i])
		}
	}
	log.Debug("Running 'pnpm " + strings.Join(pnpmInstallArgs, " ") + "' command.")
	command := exec.Command(executablePath, args...)
	command.Dir = srcPath
	outBuffer := bytes.NewBuffer([]byte{})
	command.Stdout = outBuffer
	errBuffer := bytes.NewBuffer([]byte{})
	command.Stderr = errBuffer
	err = command.Run()
	errResult = errBuffer.Bytes()
	stdResult = outBuffer.Bytes()
	if err != nil {
		err = fmt.Errorf("error while running '%s %s': %s\n%s", executablePath, strings.Join(args, " "), err.Error(), strings.TrimSpace(string(errResult)))
		return
	}
	log.Verbose("pnpm '" + strings.Join(args, " ") + "' standard output is:\n" + strings.TrimSpace(string(stdResult)))
	return
}

// This function appends the Pnpm command as the first element in pnpmArgs strings array.
// For example, if pnpmArgs equals {"--json", "--all"}, and we call appendPnpmCommand(pnpmArgs, "ls"), we will get pnpmArgs = {"ls", "--json", "--all"}.
func AppendPnpmCommand(pnpmArgs []string, command string) []string {
	termpArgs := []string{command}
	termpArgs = append(termpArgs, pnpmArgs...)
	return termpArgs
}

func GetPnpmVersionAndExecPath(log utils.Log) (*version.Version, string, error) {
	if log == nil {
		log = &utils.NullLog{}
	}
	pnpmExecPath, err := exec.LookPath("pnpm")
	if err != nil {
		return nil, "", err
	}

	if pnpmExecPath == "" {
		return nil, "", errors.New("could not find the 'pnpm' executable in the system PATH")
	}

	log.Debug("Using pnpm executable:", pnpmExecPath)

	versionData, _, err := RunPnpmCmd(pnpmExecPath, "", []string{"--version"}, log)
	if err != nil {
		return nil, "", err
	}
	return version.NewVersion(strings.TrimSpace(string(versionData))), pnpmExecPath, nil
}

// Set the pnpm store-dir configuration in the project's .npmrc file.
// This allows subsequent pnpm commands to use the specified store directory.
func SetPnpmConfigCache(srcPath, storePath string, log utils.Log) error {
	npmrcPath := filepath.Join(srcPath, ".npmrc")
	content := fmt.Sprintf("store-dir=%s\n", storePath)

	log.Debug("Setting pnpm store-dir to:", storePath)

	// Append to existing .npmrc or create new one
	f, err := os.OpenFile(npmrcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open .npmrc file: %s", err.Error())
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write to .npmrc file: %s", err.Error())
	}

	return nil
}

// Return the pnpm cache path.
// Default: Windows: %LocalAppData%\pnpm\store\vN, Posix: ~/.local/share/pnpm/store/vN
func GetPnpmConfigCache(srcPath, executablePath string, pnpmArgs []string, log utils.Log) (string, error) {
	pnpmArgs = append([]string{"store", "path"}, pnpmArgs...)
	data, errData, err := RunPnpmCmd(executablePath, srcPath, pnpmArgs, log)
	if err != nil {
		return "", err
	} else if len(errData) > 0 {
		// Some warnings and messages of pnpm are printed to stderr. They don't cause the command to fail, but we'd want to show them to the user.
		log.Warn("Encountered some issues while running 'pnpm store path' command:\n" + string(errData))
	}
	// this return value includes a 'v3' at the end of the cache path that was set
	cachePath := filepath.Join(strings.Trim(string(data), "\n"), "files")
	found, err := utils.IsDirExists(cachePath, true)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("files folder is not found in '" + cachePath + "'. Hint: Delete node_modules directory and run pnpm install or pnpm install --lockfile-only.")
	}
	return cachePath, nil
}
