package server

import (
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/h4-poc/service/pkg/config"
	"github.com/h4-poc/service/pkg/handler"
)

func NewRunCommand() *cobra.Command {
	var configFile string
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run the server",
		Long:  `Run the Application API server`,
		Run:   runServer,
	}
	runCmd.Flags().StringVarP(&configFile, "config", "c", "", "Path to config file")
	err := runCmd.MarkFlagRequired("config")
	if err != nil {
		panic(err)
	}
	return runCmd
}

func runServer(cmd *cobra.Command, args []string) {
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("Panic: %v", r)
		}
	}()

	log.SetFormatter(&log.TextFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		log.Fatalf("Failed to get config file: %v", err)
	}

	_, err = config.ParseConfig(configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	r := gin.Default()

	v1 := r.Group("/api/v1")
	// deploy/DestinationCluster
	{
		v1.GET("destinationCluster", handler.ListDestinationCluster)
		v1.POST("destinationCluster", handler.CreateDestinationCluster)
		v1.PATCH("destinationCluster/:name", handler.UpdateDestinationCluster)
	}

	// deploy/application template (only kustomization)
	{
		v1.POST("applications/template", handler.CreateApplicationTemplate)
		v1.GET("applications/templates", handler.ListApplicationTemplate)
		v1.PATCH("applications/templates", handler.UpdateApplicationTemplate)
		v1.POST("applications/templates", handler.VlidateApplicationTemplate)
	}
	// deploy/argoapplication
	{
		// group operator
		v1.POST("deploy/applications", handler.CreateArgoApplication)
		v1.GET("deploy/applications", handler.ListArgoApplications)

		// dry run
		v1.POST("deploy/argo/applications/dryrun", handler.DryRunArgoApplications)

		// one application operator
		v1.GET("deploy/argo/applications/:appName", handler.DescribeArgoApplications) // TODO
		v1.PUT("deploy/argo/applications/:appName", handler.UpdateArgoApplication)    // TODO
		v1.DELETE("deploy/argo/applications/:appName", handler.DeleteArgoApplication)
		v1.POST("deploy/argo/applications/:appName/sync", handler.DryRunArgoApplications)
		// project === tenant
		{
			v1.POST("/projects", handler.CreateProject)
			v1.GET("/projects", handler.ListProjects)
			v1.DELETE("/projects", handler.DeleteProject)
		}
	}
	// security
	{
		v1.POST("security/externalsecrets/secretstore", handler.CreateSecretStore)
		v1.GET("security/externalsecrets/secretstore", handler.ListSecretStore)
	}
	r.GET("/healthz", handler.Healthz)

	serverPort := viper.GetInt("server.port")
	serverAddr := fmt.Sprintf(":%d", serverPort)
	log.Printf("Starting server on %s", serverAddr)
	err = r.Run(serverAddr)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
