package writer

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	argocdv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	esv1beta1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1beta1"
	"github.com/ghodss/yaml"
	billyUtils "github.com/go-git/go-billy/v5/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/squidflow/service/pkg/application"
	"github.com/squidflow/service/pkg/fs"
	"github.com/squidflow/service/pkg/git"
	"github.com/squidflow/service/pkg/log"
	"github.com/squidflow/service/pkg/store"
	"github.com/squidflow/service/pkg/types"
	"github.com/squidflow/service/pkg/util"
)

var (
	DefaultApplicationSetGeneratorInterval int64 = 20

	//go:embed assets/cluster_res_readme.md
	clusterResReadmeTpl []byte
)

var _ MetaRepoWriter = &NativeRepoTarget{}

// NativeRepoTarget implements the native GitOps repository structure
type NativeRepoTarget struct {
	project             string
	metaRepoCloneOpts   *git.CloneOptions
	tenantRepoCloneOpts *git.CloneOptions
}

// RunAppCreate creates an application in the native GitOps repository structure
// this function need 3 repos:
// 1. meta repo: the project, or application (rendered)
// 2. tenant repo: no include project, only application
// 3. apps repo: the repo that contains the application description
func (n *NativeRepoTarget) RunAppCreate(ctx context.Context, opts *application.AppCreateOptions) (*types.ApplicationCreatedResp, error) {
	var (
		tenantRepo git.Repository
		appsfs     fs.FS
	)
	if n.tenantRepoCloneOpts == nil {
		n.tenantRepoCloneOpts = n.metaRepoCloneOpts
	}

	metaRepo, metaRepofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, opts.ProjectName)
	if err != nil {
		return nil, err
	}

	if n.metaRepoCloneOpts.Repo != n.tenantRepoCloneOpts.Repo {
		tenantRepo, appsfs, err = getRepo(ctx, n.tenantRepoCloneOpts)
		if err != nil {
			log.G().Errorf("failed to prepare tenant repo: %v", err)
			return nil, err
		}
	} else {
		tenantRepo, appsfs = metaRepo, metaRepofs
	}

	if err = setAppOptsDefaults(ctx, metaRepofs, opts); err != nil {
		log.G().Errorf("failed to set app opts defaults: %v", err)
		return nil, err
	}

	if opts.DryRun {
		envs := opts.AppOpts.AppSource.DetectEnvironments()
		manifests := []types.ApplicationDryRunEnv{}
		for _, env := range envs {
			manifest, err := opts.AppOpts.AppSource.Manifest(env)
			if err != nil {
				return nil, fmt.Errorf("failed to get manifest for environment %s: %w", env, err)
			}
			manifests = append(manifests, types.ApplicationDryRunEnv{
				Environment: env,
				IsValid:     true,
				Manifest:    string(manifest),
				ArgocdFile:  "",
				Error:       "",
			})
		}

		return &types.ApplicationCreatedResp{
			Success:      true,
			Message:      "dry run success",
			Total:        len(envs),
			Environments: manifests,
		}, nil
	}

	app, err := parseApp(opts.AppOpts, opts.ProjectName, n.tenantRepoCloneOpts.URL(), n.tenantRepoCloneOpts.Revision(), n.tenantRepoCloneOpts.Path())
	if err != nil {
		return nil, fmt.Errorf("failed to parse application from flags: %w", err)
	}

	if err = app.CreateFiles(metaRepofs, appsfs, opts.ProjectName); err != nil {
		if errors.Is(err, application.ErrAppAlreadyInstalledOnProject) {
			return nil, fmt.Errorf("application '%s' already exists in project '%s': %w", app.Name(), opts.ProjectName, err)
		}
		log.G().WithFields(log.Fields{
			"error": err,
		}).Error("failed to create application files")
		return nil, err
	}

	if n.metaRepoCloneOpts.Repo != n.tenantRepoCloneOpts.Repo {
		commitMsg := genCommitMsg("chore: "+
			types.ActionTypeCreate,
			types.ResourceNameApp,
			opts.AppOpts.AppName,
			opts.ProjectName,
			appsfs,
		)
		log.G().WithFields(
			log.Fields{
				"commit msg": commitMsg,
				"repo":       n.tenantRepoCloneOpts.Repo,
				"path":       n.tenantRepoCloneOpts.Path(),
			}).Debug("push to tenant repo with commit msg")
		if _, err = tenantRepo.Persist(ctx, &git.PushOptions{CommitMsg: commitMsg}); err != nil {
			return nil, fmt.Errorf("failed to push to apps repo: %w", err)
		}
		return &types.ApplicationCreatedResp{
			Success: true,
			Message: "application created",
			Total:   1,
			Environments: []types.ApplicationDryRunEnv{
				{
					Environment: "default",
					IsValid:     true,
					Manifest:    "",
					ArgocdFile:  "",
					Error:       "",
				},
			},
		}, nil
	}

	commitMsg := genCommitMsg("chore: "+
		types.ActionTypeCreate,
		types.ResourceNameApp,
		opts.AppOpts.AppName,
		opts.ProjectName,
		metaRepofs,
	)
	log.G().WithFields(log.Fields{
		"commit msg": commitMsg,
		"repo":       n.metaRepoCloneOpts.Repo,
		"path":       n.metaRepoCloneOpts.Path(),
	}).Debug("push to gitops repo with commit msg")
	_, err = metaRepo.Persist(ctx, &git.PushOptions{CommitMsg: commitMsg})
	if err != nil {
		return nil, fmt.Errorf("failed to push to gitops repo: %w", err)
	}

	return &types.ApplicationCreatedResp{
		Success: true,
		Message: "application created",
		Total:   1,
		Environments: []types.ApplicationDryRunEnv{
			{
				Environment: "default",
				IsValid:     true,
				Manifest:    "",
				ArgocdFile:  "",
				Error:       "",
			},
		},
	}, nil
}

// RunAppDelete deletes an application from the native GitOps repository structure
func (n *NativeRepoTarget) RunAppDelete(ctx context.Context, appName string) error {
	r, repofs, err := getRepo(ctx, n.tenantRepoCloneOpts)
	if err != nil {
		log.G().Errorf("failed to prepare repo: %v", err)
		return err
	}

	appDir := repofs.Join(store.Default.AppsDir, appName)
	appExists := repofs.ExistsOrDie(appDir)
	if !appExists {
		return fmt.Errorf("application '%s' not found", appName)
	}

	var dirToRemove string
	commitMsg := fmt.Sprintf("chore: delete app '%s'", appName)

	if n.project == "" {
		log.G().Debug("deleting all application from all of the project")
		dirToRemove = appDir
	} else {
		appOverlaysDir := repofs.Join(appDir, store.Default.OverlaysDir)
		overlaysExists := repofs.ExistsOrDie(appOverlaysDir)
		if !overlaysExists {
			appOverlaysDir = appDir
		}

		appProjectDir := repofs.Join(appOverlaysDir, n.project)
		overlayExists := repofs.ExistsOrDie(appProjectDir)
		if !overlayExists {
			return fmt.Errorf("application '%s' not found in project '%s'", appName, n.project)
		}

		allOverlays, err := repofs.ReadDir(appOverlaysDir)
		if err != nil {
			return fmt.Errorf("failed to read overlays directory '%s': %w", appOverlaysDir, err)
		}

		if len(allOverlays) == 1 {
			dirToRemove = appDir
		} else {
			commitMsg += fmt.Sprintf(" from project '%s'", n.project)
			dirToRemove = appProjectDir
		}
	}

	err = billyUtils.RemoveAll(repofs, dirToRemove)
	if err != nil {
		return fmt.Errorf("failed to delete directory '%s': %w", dirToRemove, err)
	}

	log.G().Info("committing changes to gitops repo...")
	if _, err = r.Persist(ctx, &git.PushOptions{CommitMsg: commitMsg}); err != nil {
		return fmt.Errorf("failed to push to repo: %w", err)
	}

	return nil
}

// RunAppList lists all applications in the native GitOps repository structure
func (n *NativeRepoTarget) RunAppList(ctx context.Context) ([]types.Application, error) {
	// Note: if with pull request mode tenantRepoCloneOpts === metaRepoCloneOpts
	// use tenantRepoCloneOpts still correct
	_, repofs, err := getRepo(ctx, n.tenantRepoCloneOpts)
	if err != nil {
		return nil, err
	}

	// if tenant's application save with meta repo path, use `apps/{appname}/overlay/{tenant}` repo path
	// else use `apps/{appname}/{tenant}` repo path
	var path = repofs.Join(store.Default.AppsDir, "*", store.Default.OverlaysDir, n.project)
	if n.tenantRepoCloneOpts.Repo != n.metaRepoCloneOpts.Repo {
		path = repofs.Join(store.Default.AppsDir, "*", n.project)
	}

	log.G().WithFields(log.Fields{
		"repo": n.tenantRepoCloneOpts.Repo,
		"path": path,
	}).Debug("listing applications")

	matches, err := billyUtils.Glob(repofs, path)
	if err != nil {
		return nil, fmt.Errorf("failed to run glob on %s: %w", n.project, err)
	}

	applications := make([]types.Application, 0, len(matches))

	for _, appPath := range matches {
		conf, err := getConfigFileFromPath(repofs, appPath)
		if err != nil {
			return nil, err
		}

		applications = append(applications, types.Application{
			ApplicationSource: types.ApplicationSourceRequest{
				Repo:           conf.SrcRepoURL,
				Path:           conf.SrcPath,
				TargetRevision: conf.SrcTargetRevision,
			},
			ApplicationInstantiation: types.ApplicationInstantiation{
				ApplicationName: conf.UserGivenName,
				TenantName:      n.project,
				AppCode:         conf.Annotations["squidflow.github.io/appcode"],
				Description:     conf.Annotations["squidflow.github.io/description"],
			},
			ApplicationTarget: []types.ApplicationTarget{
				{
					Cluster:   "in-cluster",
					Namespace: conf.DestNamespace,
				},
			},
			// note: will update later
			ApplicationRuntime: types.ApplicationRuntime{
				GitInfo:         []types.GitInfo{},
				ResourceMetrics: types.ResourceMetricsInfo{},
				Status:          "unknown",
				Health:          "unknown",
				SyncStatus:      "unknown",
				ArgoCDUrl:       "",
				CreatedAt:       time.Now(),
				CreatedBy:       "",
				LastUpdatedAt:   time.Now(),
				LastUpdatedBy:   "",
			},
		})
	}

	return applications, nil
}

// RunAppUpdate updates an application in the native GitOps repository structure
func (n *NativeRepoTarget) RunAppUpdate(ctx context.Context, opts *types.UpdateOptions) error {
	return nil
}

// RunAppGet gets an application from the native GitOps repository structure
func (n *NativeRepoTarget) RunAppGet(ctx context.Context, appName string) (*types.Application, error) {
	_, repofs, err := getRepo(ctx, n.tenantRepoCloneOpts)
	if err != nil {
		log.G().Errorf("failed to prepare repo: %v", err)
		return nil, err
	}

	appPath := repofs.Join(store.Default.AppsDir, appName, store.Default.OverlaysDir, n.project)
	if n.tenantRepoCloneOpts.Repo != n.metaRepoCloneOpts.Repo {
		appPath = repofs.Join(store.Default.AppsDir, appName, n.project)
	}

	log.G().WithFields(log.Fields{
		"repo": n.tenantRepoCloneOpts.Repo,
		"path": appPath,
	}).Debug("getting application detail")

	conf, err := getConfigFileFromPath(repofs, appPath)
	if err != nil {
		log.G().Errorf("failed to get application detail: %v", err)
		return nil, err
	}

	return &types.Application{
		ApplicationSource: types.ApplicationSourceRequest{
			Repo:           conf.SrcRepoURL,
			Path:           conf.SrcPath,
			TargetRevision: conf.SrcTargetRevision,
		},
		ApplicationInstantiation: types.ApplicationInstantiation{
			ApplicationName: conf.UserGivenName,
			TenantName:      n.project,
			AppCode:         conf.Annotations["squidflow.github.io/appcode"],
			Description:     conf.Annotations["squidflow.github.io/description"],
		},
		ApplicationTarget: []types.ApplicationTarget{
			{
				Cluster:   "in-cluster",
				Namespace: conf.DestNamespace,
			},
		},
		ApplicationRuntime: types.ApplicationRuntime{
			GitInfo:         []types.GitInfo{},
			ResourceMetrics: types.ResourceMetricsInfo{},
			Status:          "unknown",
			Health:          "unknown",
			SyncStatus:      "unknown",
			ArgoCDUrl:       "",
			CreatedAt:       time.Now(),
			CreatedBy:       "",
			LastUpdatedAt:   time.Now(),
			LastUpdatedBy:   "",
		},
	}, nil
}

// RunProjectCreate creates a project in the native GitOps repository structure
func (n *NativeRepoTarget) RunProjectCreate(ctx context.Context, opts *types.ProjectCreateOptions) error {
	var (
		err                   error
		installationNamespace string
	)

	r, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, "")
	if err != nil {
		return err
	}

	installationNamespace, err = getInstallationNamespace(repofs)
	if err != nil {
		return fmt.Errorf(util.Doc("Bootstrap folder not found, please execute `<BIN> repo bootstrap --installation-path %s` command"), repofs.Root())
	}

	projectExists := repofs.ExistsOrDie(repofs.Join(store.Default.ProjectsDir, opts.ProjectName+".yaml"))
	if projectExists {
		return fmt.Errorf("project '%s' already exists", opts.ProjectName)
	}

	if opts.DestKubeServer == "" {
		opts.DestKubeServer = store.Default.DestServer
		if opts.DestKubeContext != "" {
			opts.DestKubeServer, err = util.KubeContextToServer(opts.DestKubeContext)
			if err != nil {
				return err
			}
		}
	}

	projectYAML, appsetYAML, clusterResReadme, clusterResConf, err := generateProjectManifests(&types.GenerateProjectOptions{
		Name:               opts.ProjectName,
		Namespace:          installationNamespace,
		ProjectGitopsRepo:  opts.ProjectGitopsRepo,
		RepoURL:            n.metaRepoCloneOpts.URL(),
		Revision:           n.metaRepoCloneOpts.Revision(),
		InstallationPath:   n.metaRepoCloneOpts.Path(),
		DefaultDestServer:  opts.DestKubeServer,
		DefaultDestContext: opts.DestKubeContext,
		Labels:             opts.Labels,
		Annotations:        opts.Annotations,
	})
	if err != nil {
		return fmt.Errorf("failed to generate project resources: %w", err)
	}

	if opts.DryRun {
		log.G().Printf("%s", util.JoinManifests(projectYAML, appsetYAML))
		return nil
	}

	bulkWrites := []fs.BulkWriteRequest{}

	if opts.DestKubeContext != "" {
		log.G().Infof("adding cluster: %s", opts.DestKubeContext)
		if err = opts.AddCmd.Execute(ctx, opts.DestKubeContext); err != nil {
			return fmt.Errorf("failed to add new cluster credentials: %w", err)
		}

		if !repofs.ExistsOrDie(repofs.Join(store.Default.BootsrtrapDir, store.Default.ClusterResourcesDir, opts.DestKubeContext)) {
			bulkWrites = append(bulkWrites, fs.BulkWriteRequest{
				Filename: repofs.Join(store.Default.BootsrtrapDir, store.Default.ClusterResourcesDir, opts.DestKubeContext+".json"),
				Data:     clusterResConf,
				ErrMsg:   "failed to write cluster config",
			})

			bulkWrites = append(bulkWrites, fs.BulkWriteRequest{
				Filename: repofs.Join(store.Default.BootsrtrapDir, store.Default.ClusterResourcesDir, opts.DestKubeContext, "README.md"),
				Data:     clusterResReadme,
				ErrMsg:   "failed to write cluster resources readme",
			})
		}
	}

	bulkWrites = append(bulkWrites, fs.BulkWriteRequest{
		Filename: repofs.Join(store.Default.ProjectsDir, opts.ProjectName+".yaml"),
		Data:     util.JoinManifests(projectYAML, appsetYAML),
		ErrMsg:   "failed to create project file",
	})

	if err = fs.BulkWrite(repofs, bulkWrites...); err != nil {
		return err
	}

	log.G().Infof("pushing new project manifest to repo")
	if _, err = r.Persist(ctx, &git.PushOptions{CommitMsg: fmt.Sprintf("chore: added project '%s'", opts.ProjectName)}); err != nil {
		return err
	}

	log.G().Infof("project created: '%s'", opts.ProjectName)

	return nil
}

func generateProjectManifests(o *types.GenerateProjectOptions) (projectYAML, appSetYAML, clusterResReadme, clusterResConfig []byte, err error) {
	project := &argocdv1alpha1.AppProject{
		TypeMeta: metav1.TypeMeta{
			Kind:       argocdv1alpha1.AppProjectSchemaGroupVersionKind.Kind,
			APIVersion: argocdv1alpha1.AppProjectSchemaGroupVersionKind.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Name,
			Namespace: o.Namespace,
			Annotations: map[string]string{
				"argocd.argoproj.io/sync-wave":     "-2",
				"argocd.argoproj.io/sync-options":  "PruneLast=true",
				store.Default.DestServerAnnotation: o.DefaultDestServer,
			},
		},
		Spec: argocdv1alpha1.AppProjectSpec{
			SourceRepos: []string{"*"},
			Destinations: []argocdv1alpha1.ApplicationDestination{
				{
					Server:    "*",
					Namespace: "*",
				},
			},
			Description: fmt.Sprintf("%s project", o.Name),
			ClusterResourceWhitelist: []metav1.GroupKind{
				{
					Group: "*",
					Kind:  "*",
				},
			},
			NamespaceResourceWhitelist: []metav1.GroupKind{
				{
					Group: "*",
					Kind:  "*",
				},
			},
		},
	}
	if projectYAML, err = yaml.Marshal(project); err != nil {
		err = fmt.Errorf("failed to marshal AppProject: %w", err)
		return
	}

	appsetRepoURL := o.ProjectGitopsRepo
	if appsetRepoURL == "" {
		appsetRepoURL = o.RepoURL
	}

	appSetYAML, err = createAppSet(&createAppSetOptions{
		name:                        o.Name,
		namespace:                   o.Namespace,
		appName:                     fmt.Sprintf("%s-{{ userGivenName }}", o.Name),
		appNamespace:                o.Namespace,
		appProject:                  o.Name,
		repoURL:                     "{{ srcRepoURL }}",
		srcPath:                     "{{ srcPath }}",
		revision:                    "{{ srcTargetRevision }}",
		destServer:                  "{{ destServer }}",
		destNamespace:               "{{ destNamespace }}",
		prune:                       true,
		preserveResourcesOnDeletion: false,
		appLabels:                   getDefaultAppLabels(o.Labels),
		appAnnotations:              o.Annotations,
		generators: []argocdv1alpha1.ApplicationSetGenerator{
			{
				Git: &argocdv1alpha1.GitGenerator{
					RepoURL:  appsetRepoURL, // use tenant's repo url
					Revision: o.Revision,
					Files: []argocdv1alpha1.GitFileGeneratorItem{
						{
							Path: path.Join(o.InstallationPath, store.Default.AppsDir, "**", o.Name, "config.json"),
						},
					},
					RequeueAfterSeconds: &DefaultApplicationSetGeneratorInterval,
				},
			},
			{
				Git: &argocdv1alpha1.GitGenerator{
					RepoURL:  appsetRepoURL, // use tenant's repo url
					Revision: o.Revision,
					Files: []argocdv1alpha1.GitFileGeneratorItem{
						{
							Path: path.Join(o.InstallationPath, store.Default.AppsDir, "**", o.Name, "config_dir.json"),
						},
					},
					RequeueAfterSeconds: &DefaultApplicationSetGeneratorInterval,
					Template: argocdv1alpha1.ApplicationSetTemplate{
						Spec: argocdv1alpha1.ApplicationSpec{
							Source: &argocdv1alpha1.ApplicationSource{
								Directory: &argocdv1alpha1.ApplicationSourceDirectory{
									Recurse: true,
									Exclude: "{{ exclude }}",
									Include: "{{ include }}",
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		err = fmt.Errorf("failed to marshal ApplicationSet: %w", err)
		return
	}

	clusterResReadme = []byte(strings.ReplaceAll(string(clusterResReadmeTpl), "{CLUSTER}", o.DefaultDestServer))

	clusterResConfig, err = json.Marshal(&application.ClusterResConfig{Name: o.DefaultDestContext, Server: o.DefaultDestServer})
	if err != nil {
		err = fmt.Errorf("failed to create cluster resources config: %w", err)
		return
	}

	return
}

type createAppSetOptions struct {
	name                        string
	namespace                   string
	appName                     string
	appNamespace                string
	appProject                  string
	repoURL                     string
	revision                    string
	srcPath                     string
	destServer                  string
	destNamespace               string
	prune                       bool
	preserveResourcesOnDeletion bool
	appLabels                   map[string]string
	appAnnotations              map[string]string
	generators                  []argocdv1alpha1.ApplicationSetGenerator
}

func createAppSet(o *createAppSetOptions) ([]byte, error) {
	if o.destServer == "" {
		o.destServer = store.Default.DestServer
	}

	if o.appProject == "" {
		o.appProject = "default"
	}

	if o.appLabels == nil {
		// default labels
		o.appLabels = map[string]string{
			store.Default.LabelKeyAppManagedBy: store.Default.LabelValueManagedBy,
			"app.kubernetes.io/name":           o.appName,
		}
	}

	appSet := &argocdv1alpha1.ApplicationSet{
		TypeMeta: metav1.TypeMeta{
			// do not use argocdv1alpha1.ApplicationSetSchemaGroupVersionKind.Kind because
			// it is "Applicationset" - noticed the lowercase "s"
			Kind:       "ApplicationSet",
			APIVersion: argocdv1alpha1.ApplicationSetSchemaGroupVersionKind.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.name,
			Namespace: o.namespace,
			Annotations: map[string]string{
				"argocd.argoproj.io/sync-wave": "0",
			},
		},
		Spec: argocdv1alpha1.ApplicationSetSpec{
			Generators: o.generators,
			Template: argocdv1alpha1.ApplicationSetTemplate{
				ApplicationSetTemplateMeta: argocdv1alpha1.ApplicationSetTemplateMeta{
					Namespace:   o.appNamespace,
					Name:        o.appName,
					Labels:      o.appLabels,
					Annotations: o.appAnnotations,
				},
				Spec: argocdv1alpha1.ApplicationSpec{
					Project: o.appProject,
					Source: &argocdv1alpha1.ApplicationSource{
						RepoURL:        o.repoURL,
						Path:           o.srcPath,
						TargetRevision: o.revision,
					},
					Destination: argocdv1alpha1.ApplicationDestination{
						Server:    o.destServer,
						Namespace: o.destNamespace,
					},
					SyncPolicy: &argocdv1alpha1.SyncPolicy{
						Automated: &argocdv1alpha1.SyncPolicyAutomated{
							SelfHeal:   true,
							Prune:      o.prune,
							AllowEmpty: true,
						},
					},
					IgnoreDifferences: []argocdv1alpha1.ResourceIgnoreDifferences{
						{
							Group: "argoproj.io",
							Kind:  "Application",
							JSONPointers: []string{
								"/status",
							},
						},
					},
				},
			},
			SyncPolicy: &argocdv1alpha1.ApplicationSetSyncPolicy{
				PreserveResourcesOnDeletion: o.preserveResourcesOnDeletion,
			},
		},
	}

	return yaml.Marshal(appSet)
}

func (n *NativeRepoTarget) SecretStoreList(ctx context.Context) ([]esv1beta1.SecretStore, error) {
	_, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, "")
	if err != nil {
		return nil, err
	}

	matches, err := billyUtils.Glob(repofs, repofs.Join(
		store.Default.BootsrtrapDir,
		store.Default.ClusterResourcesDir,
		store.Default.ClusterContextName,
		"ss-*.yaml",
	))
	if err != nil {
		return nil, err
	}

	var secretStores []esv1beta1.SecretStore

	for _, file := range matches {
		log.G().WithField("file", file).Debug("Found secret store")

		secretStore := &esv1beta1.SecretStore{}
		if err := repofs.ReadYamls(file, secretStore); err != nil {
			log.G().Warnf("Failed to read secret store from %s: %v", file, err)
			continue
		}

		if secretStore.Kind != "SecretStore" {
			log.G().Warnf("Skip %s: not a SecretStore", file)
			continue
		}

		log.G().WithFields(log.Fields{
			"id":       secretStore.Annotations["squidflow.github.io/id"],
			"name":     secretStore.Name,
			"provider": "vault",
		}).Debug("Found secret store")

		secretStores = append(secretStores, *secretStore)
	}

	return secretStores, nil
}

// WriteSecretStore2Repo the external secret to gitOps repo
func (n *NativeRepoTarget) SecretStoreCreate(ctx context.Context, ss *esv1beta1.SecretStore, force bool) error {
	log.G().WithFields(log.Fields{
		"name":      ss.Name,
		"id":        ss.Annotations["squidflow.github.io/id"],
		"cloneOpts": n.metaRepoCloneOpts,
		"force":     force,
	}).Debug("clone options")

	r, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, "")
	if err != nil {
		log.G().WithError(err).Error("failed to prepare repo")
		return err
	}

	ssYaml, err := yaml.Marshal(ss)
	if err != nil {
		log.G().WithError(err).Error("failed to marshal secret store")
		return err
	}

	ssExists := repofs.ExistsOrDie(
		repofs.Join(
			store.Default.BootsrtrapDir,
			store.Default.ClusterResourcesDir,
			store.Default.ClusterContextName,
			fmt.Sprintf("ss-%s.yaml", ss.Annotations["squidflow.github.io/id"]),
		),
	)
	if ssExists && !force {
		return fmt.Errorf("secret store '%s' already exists", ss.GetName())
	}

	bulkWrites := []fs.BulkWriteRequest{}
	bulkWrites = append(bulkWrites, fs.BulkWriteRequest{
		Filename: repofs.Join(
			store.Default.BootsrtrapDir,
			store.Default.ClusterResourcesDir,
			store.Default.ClusterContextName,
			fmt.Sprintf("ss-%s.yaml", ss.Annotations["squidflow.github.io/id"]),
		),
		Data:   util.JoinManifests(ssYaml),
		ErrMsg: "failed to create secret store file",
	})

	if err = fs.BulkWrite(repofs, bulkWrites...); err != nil {
		return err
	}

	if _, err = r.Persist(ctx, &git.PushOptions{CommitMsg: fmt.Sprintf("chore: added secret store '%s'", ss.GetName())}); err != nil {
		log.G().WithError(err).Error("failed to push secret store to repo")
		return err
	}

	log.G().Infof("secret store created: '%s'", ss.GetName())

	return nil
}

func (n *NativeRepoTarget) SecretStoreUpdate(ctx context.Context, id string, req *types.SecretStoreUpdateRequest) (*esv1beta1.SecretStore, error) {
	_, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, "")
	if err != nil {
		return nil, fmt.Errorf("failed to prepare repo: %w", err)
	}

	secretStorePath := repofs.Join(
		store.Default.BootsrtrapDir,
		store.Default.ClusterResourcesDir,
		store.Default.ClusterContextName,
		fmt.Sprintf("ss-%s.yaml", id),
	)

	secretStore := &esv1beta1.SecretStore{}
	if err := repofs.ReadYamls(secretStorePath, secretStore); err != nil {
		return nil, fmt.Errorf("failed to read secret store: %w", err)
	}

	// Update fields
	if req.Name != "" {
		secretStore.Name = req.Name
	}
	if req.Path != "" {
		secretStore.Spec.Provider.Vault.Path = &req.Path
	}
	if req.Auth != nil {
		secretStore.Spec.Provider.Vault.Auth = *req.Auth
	}
	if req.Server != "" {
		secretStore.Spec.Provider.Vault.Server = req.Server
	}

	secretStore.Annotations["squidflow.github.io/updated-at"] = time.Now().Format(time.RFC3339)

	if err := n.SecretStoreCreate(ctx, secretStore, true); err != nil {
		return nil, fmt.Errorf("failed to write secret store to repo: %w", err)
	}

	return secretStore, nil
}

func (n *NativeRepoTarget) SecretStoreDelete(ctx context.Context, secretStoreID string) error {
	r, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, "")
	if err != nil {
		return err
	}

	secretStorePath := repofs.Join(
		store.Default.BootsrtrapDir,
		store.Default.ClusterResourcesDir,
		store.Default.ClusterContextName,
		fmt.Sprintf("ss-%s.yaml", secretStoreID),
	)

	exists := repofs.ExistsOrDie(secretStorePath)
	if !exists {
		log.G().Infof("secret store %s not found, considering it as already deleted", secretStoreID)
		return nil
	}

	if err := repofs.Remove(secretStorePath); err != nil {
		return fmt.Errorf("failed to delete secret store file: %v", err)
	}

	if _, err = r.Persist(ctx, &git.PushOptions{
		CommitMsg: fmt.Sprintf("chore: deleted secret store '%s'", secretStoreID),
	}); err != nil {
		return fmt.Errorf("failed to push secret store deletion to repo: %v", err)
	}

	log.G().Infof("secret store deleted: '%s'", secretStoreID)
	return nil
}

func (n *NativeRepoTarget) SecretStoreGet(ctx context.Context, id string) (*esv1beta1.SecretStore, error) {
	_, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, "")
	if err != nil {
		return nil, err
	}

	secretStorePath := repofs.Join(
		store.Default.BootsrtrapDir,
		store.Default.ClusterResourcesDir,
		store.Default.ClusterContextName,
		fmt.Sprintf("ss-%s.yaml", id),
	)

	secretStore := &esv1beta1.SecretStore{}
	if err := repofs.ReadYamls(secretStorePath, secretStore); err != nil {
		return nil, fmt.Errorf("failed to read secret store %s: %v", id, err)
	}

	if secretStore.Kind != "SecretStore" {
		return nil, fmt.Errorf("invalid secret store kind: %s", secretStore.Kind)
	}

	return secretStore, nil
}

var getProjectInfoFromFile = func(repofs fs.FS, name string) (*argocdv1alpha1.AppProject, *argocdv1alpha1.ApplicationSet, error) {
	proj := &argocdv1alpha1.AppProject{}
	appSet := &argocdv1alpha1.ApplicationSet{}
	if err := repofs.ReadYamls(name, proj, appSet); err != nil {
		return nil, nil, err
	}

	return proj, appSet, nil
}

// Note: all the project delete logic is based on meta repo
func (n *NativeRepoTarget) RunProjectDelete(ctx context.Context, projectName string) error {
	r, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, projectName)
	if err != nil {
		return err
	}

	allApps, err := repofs.ReadDir(store.Default.AppsDir)
	if err != nil {
		return fmt.Errorf("failed to list all applications")
	}

	for _, app := range allApps {
		err = DeleteFromProject(repofs, app.Name(), projectName)
		if err != nil {
			return err
		}
	}

	err = repofs.Remove(repofs.Join(store.Default.ProjectsDir, projectName+".yaml"))
	if err != nil {
		return fmt.Errorf("failed to delete project '%s': %w", projectName, err)
	}

	log.G().WithFields(log.Fields{"project": projectName}).Info("deleting project")
	if _, err = r.Persist(ctx, &git.PushOptions{CommitMsg: fmt.Sprintf("chore: deleted project '%s'", projectName)}); err != nil {
		return fmt.Errorf("failed to push to repo: %w", err)
	}

	return nil
}

func (n *NativeRepoTarget) RunProjectList(ctx context.Context) ([]types.TenantInfo, error) {
	n.metaRepoCloneOpts.Parse()

	_, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, "")
	if err != nil {
		return nil, err
	}

	matches, err := billyUtils.Glob(repofs, repofs.Join(store.Default.ProjectsDir, "*.yaml"))
	if err != nil {
		return nil, err
	}

	var tenants []types.TenantInfo
	for _, name := range matches {
		proj, appset, err := getProjectInfoFromFile(repofs, name)
		if err != nil {
			return nil, err
		}

		tenantInfo := types.TenantInfo{
			Name:           proj.Name,
			Namespace:      proj.Namespace,
			DefaultCluster: proj.Annotations[store.Default.DestServerAnnotation],
			GitOpsRepo:     appset.Spec.Generators[0].Git.RepoURL,
		}
		tenants = append(tenants, tenantInfo)
	}

	return tenants, nil
}

func (n *NativeRepoTarget) RunProjectGet(ctx context.Context, projectName string) (*types.TenantDetailInfo, error) {
	_, repofs, err := prepareRepo(ctx, n.metaRepoCloneOpts, projectName)
	if err != nil {
		return nil, err
	}

	projectFile := repofs.Join(store.Default.ProjectsDir, projectName+".yaml")
	if !repofs.ExistsOrDie(projectFile) {
		return nil, fmt.Errorf("project %s not found", projectName)
	}

	proj, appset, err := getProjectInfoFromFile(repofs, projectFile)
	if err != nil {
		return nil, err
	}

	detail := &types.TenantDetailInfo{
		Name:           proj.Name,
		Namespace:      proj.Namespace,
		Description:    proj.Annotations["description"],
		DefaultCluster: proj.Annotations[store.Default.DestServerAnnotation],
		CreatedBy:      proj.Annotations["created-by"],
		CreatedAt:      proj.CreationTimestamp.String(),
		GitOpsRepo:     appset.Spec.Generators[0].Git.RepoURL,
	}

	if len(proj.Spec.SourceRepos) > 0 {
		detail.SourceRepos = proj.Spec.SourceRepos
	}

	if len(proj.Spec.Destinations) > 0 {
		for _, dest := range proj.Spec.Destinations {
			detail.Destinations = append(detail.Destinations, types.ProjectDest{
				Server:    dest.Server,
				Namespace: dest.Namespace,
			})
		}
	}

	if len(proj.Spec.ClusterResourceWhitelist) > 0 {
		for _, res := range proj.Spec.ClusterResourceWhitelist {
			detail.ClusterResourceWhitelist = append(detail.ClusterResourceWhitelist, types.ProjectResource{
				Group: res.Group,
				Kind:  res.Kind,
			})
		}
	}

	if len(proj.Spec.NamespaceResourceWhitelist) > 0 {
		for _, res := range proj.Spec.NamespaceResourceWhitelist {
			detail.NamespaceResourceWhitelist = append(detail.NamespaceResourceWhitelist, types.ProjectResource{
				Group: res.Group,
				Kind:  res.Kind,
			})
		}
	}

	return detail, nil
}

func DeleteFromProject(repofs fs.FS, appName, projectName string) error {
	var dirToCheck string
	appDir := repofs.Join(store.Default.AppsDir, appName)
	appOverlay := repofs.Join(appDir, store.Default.OverlaysDir)
	if repofs.ExistsOrDie(appOverlay) {
		// kustApp
		dirToCheck = appOverlay
	} else {
		// dirApp
		dirToCheck = appDir
	}

	allProjects, err := repofs.ReadDir(dirToCheck)
	if err != nil {
		return fmt.Errorf("failed to check projects in '%s': %w", appName, err)
	}

	var found = false
	for _, project := range allProjects {
		if project.Name() == projectName {
			found = true
		}
	}
	if !found {
		return nil
	}

	var dirToRemove string
	if len(allProjects) == 1 {
		dirToRemove = appDir
		log.G().Infof("Deleting app '%s'", appName)
	} else {
		dirToRemove = repofs.Join(dirToCheck, projectName)
		log.G().Infof("Deleting app '%s' from project '%s'", appName, projectName)
	}

	err = billyUtils.RemoveAll(repofs, dirToRemove)
	if err != nil {
		return fmt.Errorf("failed to delete directory '%s': %w", dirToRemove, err)
	}

	return nil
}
