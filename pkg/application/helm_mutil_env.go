package application

import (
	"fmt"
	"path"

	v1 "k8s.io/api/core/v1"

	"github.com/squidflow/service/pkg/application/dryrun"
	"github.com/squidflow/service/pkg/fs"
	"github.com/squidflow/service/pkg/git"
	"github.com/squidflow/service/pkg/log"
	"github.com/squidflow/service/pkg/store"
	"github.com/squidflow/service/pkg/util"
)

type helmMultiEnvApp struct {
	baseApp
	name      string
	namespace *v1.Namespace
	config    *Config
	err       map[string]error  // key is the env name
	manifests map[string][]byte // key is the env name
}

func newHelmMultiEnvApp(o *CreateOptions, projectName, repoURL, targetRevision, repoRoot string) (*helmMultiEnvApp, error) {
	var err error

	app := &helmMultiEnvApp{
		baseApp: baseApp{o},
	}

	if o.AppSpecifier == "" {
		return nil, ErrEmptyAppSpecifier
	}

	if o.AppName == "" {
		o.AppName = "default"
	}

	if projectName == "" {
		return nil, ErrEmptyProjectName
	}

	if o.DestNamespace == "" {
		o.DestNamespace = "default"
	}

	if len(o.Environments) == 0 {
		return nil, fmt.Errorf("helm-multiple-env app requires at least one environment: default")
	}

	if app.manifests == nil {
		app.manifests = make(map[string][]byte)
	}

	if app.err == nil {
		app.err = make(map[string]error)
	}

	// parse git url
	_, orgRepo, appPath, _, _, _, _ := util.ParseGitUrl(o.AppSpecifier)
	log.G().WithFields(log.Fields{
		"orgRepo": orgRepo,
		"path":    appPath,
	}).Debug("parsed git url, generating helm manifests")

	_, appfs, exists := git.GetRepositoryCache().Get(orgRepo, false)
	if !exists {
		return nil, fmt.Errorf("failed to get repository cache")
	}

	if o.InstallationMode != InstallModeFlatten {
		return nil, fmt.Errorf("helm-multiple-env app does not support installation mode %s", o.InstallationMode)
	}

	for _, env := range o.Environments {
		log.G().WithFields(log.Fields{
			"path":         appPath,
			"manifestPath": o.HelmManifestPath,
			"env":          env,
			"namespace":    o.DestNamespace,
			"name":         o.AppName,
		}).Debug("helm-multiple-env app generating manifest")
		app.manifests[env], err = dryrun.GenerateHelmManifest(appfs, appPath, o.HelmManifestPath, env, o.DestNamespace, o.AppName)
		if err != nil {
			log.G().WithFields(log.Fields{
				"error": err,
			}).Error("helm-multiple-env app generating manifest")
			app.err[env] = err
			continue
		}
	}

	log.G().WithFields(log.Fields{
		"appName":           o.AppName,
		"destNamespace":     o.DestNamespace,
		"destServer":        o.DestServer,
		"srcRepoURL":        repoURL,
		"srcPath":           path.Join(repoRoot, store.Default.AppsDir, o.AppName, store.Default.OverlaysDir, projectName),
		"srcTargetRevision": targetRevision,
		"labels":            o.Labels,
		"annotations":       o.Annotations,
		"installationMode":  o.InstallationMode,
	}).Debug("helm-multi-env app creating app config")

	return app, nil
}

func (h *helmMultiEnvApp) CreateFiles(repofs fs.FS, appsfs fs.FS, projectName string) error {
	return nil
}

func (h *helmMultiEnvApp) Manifests() map[string][]byte {
	return h.manifests
}