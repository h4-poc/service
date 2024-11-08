package handler

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	clientCluster "github.com/argoproj/argo-cd/v2/pkg/apiclient/cluster"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/version"

	"github.com/h4-poc/service/pkg/argocd"
	"github.com/h4-poc/service/pkg/log"
)

type ClusterResponse struct {
	Name              string            `json:"name"`
	Environment       string            `json:"environment"`
	Status            []ComponentStatus `json:"componentStatus"`
	Provider          string            `json:"provider"`
	Version           VersionInfo       `json:"version"`
	NodeCount         int               `json:"nodeCount"`
	Region            string            `json:"region"`
	ResourceQuota     ResourceQuota     `json:"resourceQuota"`
	Health            HealthStatus      `json:"health"`
	Nodes             NodeStatus        `json:"nodes"`
	NetworkPolicy     bool              `json:"networkPolicy"`
	IngressController string            `json:"ingressController"`
	LastUpdated       string            `json:"lastUpdated"`
	ConsoleUrl        string            `json:"consoleUrl,omitempty"`
	Monitoring        MonitoringInfo    `json:"monitoring"`
	Builtin           bool              `json:"builtin,omitempty"`
}

type VersionInfo struct {
	Kubernetes string `json:"kubernetes"`
	Platform   string `json:"platform"`
}

type ResourceQuota struct {
	CPU       string `json:"cpu"`
	Memory    string `json:"memory"`
	Storage   string `json:"storage"`
	PVCs      string `json:"pvcs"`
	NodePorts string `json:"nodeports"`
}

type HealthStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type NodeStatus struct {
	Ready int `json:"ready"`
	Total int `json:"total"`
}

type MonitoringInfo struct {
	Prometheus   bool            `json:"prometheus"`
	Grafana      bool            `json:"grafana"`
	AlertManager bool            `json:"alertmanager"`
	URLs         *MonitoringURLs `json:"urls,omitempty"`
}

type MonitoringURLs struct {
	Prometheus   string `json:"prometheus,omitempty"`
	Grafana      string `json:"grafana,omitempty"`
	AlertManager string `json:"alertmanager,omitempty"`
}

type ComponentStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// ListDestinationCluster handles the GET request for listing clusters
func ListDestinationCluster(c *gin.Context) {
	argocdClient := argocd.GetArgoServerClient()
	closer, clsClient := argocdClient.NewClusterClientOrDie()
	defer closer.Close()

	clusterList, err := clsClient.List(context.Background(), &clientCluster.ClusterQuery{})
	if err != nil {
		log.G().Errorf("Failed to list clusters: %v", err)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to list clusters: %v", err)})
		return
	}

	response := []ClusterResponse{}
	log.G().WithFields(log.Fields{
		"cluster count": len(clusterList.Items),
	}).Debug("list destination cluster found clusters count")

	for _, cluster := range clusterList.Items {
		destK8sClient, err := GetDestKubernetesClient(&cluster)
		if err != nil {
			log.G().Warnf("Failed to get Kubernetes client with TLS for cluster %s: %v", cluster.Name, err)
			continue
		}

		version, err := destK8sClient.Discovery().ServerVersion()
		if err != nil {
			log.G().Errorf("Failed to get server version: %v", err)
			c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to get server version: %v", err)})
			return
		}

		total, readyNodes := countReadyNodes(destK8sClient)

		clusterInfo := ClusterResponse{
			Name:        cluster.Name,
			Environment: cluster.Labels["environment"],
			Status:      getClusterStatus(destK8sClient),
			Provider:    cluster.Labels["vendor"],
			Version: VersionInfo{
				Kubernetes: version.GitVersion,
				Platform:   getPlatformVersion(version, cluster.Labels["vendor"]),
			},
			NodeCount:     total,
			Region:        cluster.Labels["region"],
			ResourceQuota: getResourceQuota(cluster),
			Health: HealthStatus{
				Status:  cluster.Info.ConnectionState.Status,
				Message: cluster.Info.ConnectionState.Message,
			},
			Nodes: NodeStatus{
				Ready: readyNodes,
				Total: total,
			},
			NetworkPolicy:     true, // This should be determined based on cluster configuration
			IngressController: getIngressController(cluster.Labels["vendor"]),
			LastUpdated:       time.Now().String(),
			ConsoleUrl:        getConsoleURL(cluster),
			Monitoring:        getMonitoringInfo(cluster),
			Builtin:           cluster.Labels["builtin"] == "true",
		}

		response = append(response, clusterInfo)
	}

	c.JSON(200, response)
}

func getConsoleURL(cluster v1alpha1.Cluster) string {
	log.G().WithFields(log.Fields{
		"cluster name": cluster.Name,
	}).Debugf("Getting console URL for cluster")

	return "https://console.aws.amazon.com/eks/home?region=us-west-2#/clusters/" + cluster.Name
}

func getMonitoringInfo(cluster v1alpha1.Cluster) MonitoringInfo {
	log.G().WithFields(log.Fields{
		"cluster":        cluster.Name,
		"cluster labels": cluster.Labels,
	}).Debugf("Getting monitoring info for cluster")

	return MonitoringInfo{
		Prometheus:   true,
		Grafana:      true,
		AlertManager: true,
		URLs: &MonitoringURLs{
			Prometheus:   "http://prometheus.argo-cd.svc.cluster.local",
			Grafana:      "http://grafana.argo-cd.svc.cluster.local",
			AlertManager: "http://alertmanager.argo-cd.svc.cluster.local",
		},
	}
}

func getPlatformVersion(version *version.Info, vendor string) string {
	log.G().Infof("Version: %v", version)
	if vendor == "aws" {
		return version.Platform
	}
	return version.GitVersion
}

func getResourceQuota(cluster v1alpha1.Cluster) ResourceQuota {
	log.G().WithFields(log.Fields{
		"cluster name": cluster.Name,
	}).Debugf("Getting resource quota for cluster")

	return ResourceQuota{
		CPU:       "64 cores",
		Memory:    "256Gi",
		Storage:   "5000Gi",
		PVCs:      "50",
		NodePorts: "20",
	}
}

// TODO: need to implement
func getIngressController(vendor string) string {
	log.G().WithFields(log.Fields{
		"vendor": vendor,
	}).Debugf("Getting ingress controller for vendor")

	return "nginx"
}

// TODO: need to implement
func countReadyNodes(destCluster kubernetes.Interface) (total, ready int) {
	nodes, err := destCluster.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.G().Errorf("Failed to list nodes: %v", err)
		return 0, 0
	}
	readyNodes := 0
	for _, node := range nodes.Items {
		if node.Status.Conditions[0].Status == corev1.ConditionTrue {
			readyNodes++
		}
	}
	return len(nodes.Items), readyNodes
}

// TODO: need implement
func getClusterStatus(destCluster kubernetes.Interface) []ComponentStatus {
	cs, err := destCluster.CoreV1().ComponentStatuses().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return []ComponentStatus{{
			Name:    "cluster",
			Status:  "degraded",
			Message: "Failed to get component status",
			Error:   err.Error(),
		}}
	}

	var components []ComponentStatus
	for _, component := range cs.Items {
		status := ComponentStatus{
			Name: component.Name,
		}

		status.Status = "Healthy"
		status.Message = "ok"

		for _, condition := range component.Conditions {
			if condition.Status != "True" {
				status.Status = "Unhealthy"
				status.Message = condition.Message
				if condition.Error != "" {
					status.Error = condition.Error
				}
				break
			}
		}

		components = append(components, status)
	}

	return components
}
