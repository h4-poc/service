package handler

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	argocdv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	argocdv1alpha1client "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/typed/application/v1alpha1"
	"github.com/gin-gonic/gin"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	billyUtils "github.com/go-git/go-billy/v5/util"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"

	"github.com/h4-poc/service/pkg/fs"
	"github.com/h4-poc/service/pkg/git"
	"github.com/h4-poc/service/pkg/kube"
	"github.com/h4-poc/service/pkg/store"
)

type AppListOptions struct {
	CloneOpts    *git.CloneOptions
	ProjectName  string
	KubeClient   kubernetes.Interface
	ArgoCDClient *argocdv1alpha1client.ArgoprojV1alpha1Client
}

type ResourceMetrics struct {
	CPUCores    string `json:"cpu_cores"`
	MemoryUsage string `json:"memory_usage"`
}

type AppListResponse struct {
	ProjectName string              `json:"project_name"`
	Apps        []AppDetailResponse `json:"apps"`
}

type AppDetailResponse struct {
	Name          string          `json:"name"`
	DestNamespace string          `json:"dest_namespace"`
	DestServer    string          `json:"dest_server"`
	Creator       string          `json:"creator"`
	LastUpdater   string          `json:"last_updater"`
	LastCommitID  string          `json:"last_commit_id"`
	LastCommitLog string          `json:"last_commit_message"`
	PodCount      int             `json:"pod_count"`
	SecretCount   int             `json:"secret_count"`
	ResourceUsage ResourceMetrics `json:"resource_usage"`
	Status        string          `json:"status"`
	Health        string          `json:"health"`
	SyncStatus    string          `json:"sync_status"`
}

func ListArgoApplications(c *gin.Context) {
	var project string
	if c.Query("project") != "" {
		project = c.Query("project")
	}

	cloneOpts := &git.CloneOptions{
		Repo:     viper.GetString("application_repo.remote_url"),
		FS:       fs.Create(memfs.New()),
		Provider: "github",
		Auth: git.Auth{
			Password: viper.GetString("application_repo.access_token"),
		},
		CloneForWrite: false,
	}
	cloneOpts.Parse()

	// argocd client
	argoClient, err := kube.NewArgoCdClient()
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to create ArgoCD client: %v", err)})
	}

	response, err := RunAppList(context.Background(), &AppListOptions{
		CloneOpts:    cloneOpts,
		ProjectName:  project,
		ArgoCDClient: argoClient,
	})
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to list applications: %v", err)})
		return
	}

	c.JSON(200, response)
}

func RunAppList(ctx context.Context, opts *AppListOptions) (*AppListResponse, error) {
	_, repofs, err := prepareRepo(ctx, opts.CloneOpts, opts.ProjectName)
	if err != nil {
		return nil, err
	}

	// get all apps beneath apps/*/overlays/<project>
	matches, err := billyUtils.Glob(repofs, repofs.Join(store.Default.AppsDir, "*", store.Default.OverlaysDir, opts.ProjectName))
	if err != nil {
		return nil, fmt.Errorf("failed to run glob on %s: %w", opts.ProjectName, err)
	}

	response := &AppListResponse{
		ProjectName: opts.ProjectName,
		Apps:        make([]AppDetailResponse, 0),
	}

	for _, appPath := range matches {
		conf, err := getConfigFileFromPath(repofs, appPath)
		if err != nil {
			return nil, err
		}

		gitInfo, err := getGitInfo(repofs, appPath)
		if err != nil {
			log.Warnf("failed to get git info for %s: %v", appPath, err)
		}

		var (
			applicationName = opts.ProjectName + "-" + conf.UserGivenName
			applicationNs   = store.Default.ArgoCDNamespace
		)
		log.Debugf("applicationName: %s, applicationNs: %s", applicationName, applicationNs)
		argoApp, err := opts.ArgoCDClient.Applications(applicationNs).Get(ctx, applicationName, metav1.GetOptions{})
		if err != nil {
			log.Errorf("failed to get ArgoCD app info for %s: %v", conf.UserGivenName, err)
			return nil, err
		}

		resourceMetrics, err := getResourceMetrics(ctx, opts.KubeClient, conf.DestNamespace)
		if err != nil {
			log.Warnf("failed to get resource metrics for %s: %v", conf.DestNamespace, err)
		}

		app := AppDetailResponse{
			Name:          conf.UserGivenName,
			DestNamespace: conf.DestNamespace,
			DestServer:    conf.DestServer,
			Creator:       gitInfo.Creator,
			LastUpdater:   gitInfo.LastUpdater,
			LastCommitID:  gitInfo.LastCommitID,
			LastCommitLog: gitInfo.LastCommitMessage,
			PodCount:      resourceMetrics.PodCount,
			SecretCount:   resourceMetrics.SecretCount,
			ResourceUsage: ResourceMetrics{
				CPUCores:    resourceMetrics.CPU,
				MemoryUsage: resourceMetrics.Memory,
			},
			Status:     getAppStatus(argoApp),
			Health:     getAppHealth(argoApp),
			SyncStatus: getAppSyncStatus(argoApp),
		}
		response.Apps = append(response.Apps, app)
	}

	return response, nil
}

type GitInfo struct {
	Creator           string
	LastUpdater       string
	LastCommitID      string
	LastCommitMessage string
}

// TODO: Implement this function later
func getGitInfo(repofs billy.Filesystem, appPath string) (*GitInfo, error) {
	return &GitInfo{
		Creator:           "Unknown",
		LastUpdater:       "Unknown",
		LastCommitID:      "Unknown",
		LastCommitMessage: "Unknown",
	}, nil
}

type ResourceMetricsInfo struct {
	PodCount    int
	SecretCount int
	CPU         string
	Memory      string
}

// TODO: Implement this function later
func getResourceMetrics(ctx context.Context, kubeClient kubernetes.Interface, namespace string) (*ResourceMetricsInfo, error) {
	return &ResourceMetricsInfo{
		PodCount:    0,
		SecretCount: 0,
		CPU:         "0",
		Memory:      "0",
	}, nil
}

func getAppStatus(app *argocdv1alpha1.Application) string {
	if app == nil {
		return "Unknown"
	}
	log.Debugf("get app OperationState: %v", app.Status.OperationState)
	return string(app.Status.OperationState.Phase)
}

func getAppHealth(app *argocdv1alpha1.Application) string {
	if app == nil {
		return "Unknown"
	}
	log.Debugf("get app Health: %v", app.Status.Health.Status)
	return string(app.Status.Health.Status)
}

func getAppSyncStatus(app *argocdv1alpha1.Application) string {
	if app == nil {
		return "Unknown"
	}
	return string(app.Status.Sync.Status)
}
