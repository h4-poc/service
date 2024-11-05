package handler

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/go-git/go-billy/v5/memfs"
	billyUtils "github.com/go-git/go-billy/v5/util"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/h4-poc/service/pkg/fs"
	"github.com/h4-poc/service/pkg/git"
	"github.com/h4-poc/service/pkg/store"
)

type AppDeleteOptions struct {
	CloneOpts   *git.CloneOptions
	ProjectName string
	AppName     string
	Global      bool
}

// DELETE http://localhost:8080/api/v1/applications?project=testing&app=demo1
func DeleteArgoApplication(c *gin.Context) {
	projectName := c.Query("project")
	appName := c.Query("app")

	if projectName == "" || appName == "" {
		c.JSON(400, gin.H{"error": "Both project and app query parameters are required"})
		return
	}

	cloneOpts := &git.CloneOptions{
		Repo:     viper.GetString("application_repo.remote_url"),
		FS:       fs.Create(memfs.New()),
		Provider: "github",
		Auth: git.Auth{
			Password: viper.GetString("application_repo.access_token"),
		},
		CloneForWrite: true,
	}
	cloneOpts.Parse()

	opts := &AppDeleteOptions{
		CloneOpts:   cloneOpts,
		ProjectName: projectName,
		AppName:     appName,
		Global:      false, // Set to true if you want to delete the app globally
	}

	err := RunAppDelete(context.Background(), opts)
	if err != nil {
		log.Errorf("Failed to delete application: %v", err)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to delete application: %v", err)})
		return
	}

	c.JSON(200, gin.H{"message": fmt.Sprintf("Application '%s' deleted from project '%s'", appName, projectName)})
}

func RunAppDelete(ctx context.Context, opts *AppDeleteOptions) error {
	r, repofs, err := prepareRepo(ctx, opts.CloneOpts, opts.ProjectName)
	if err != nil {
		return err
	}

	appDir := repofs.Join(store.Default.AppsDir, opts.AppName)
	appExists := repofs.ExistsOrDie(appDir)
	if !appExists {
		return fmt.Errorf("application '%s' not found", opts.AppName)
	}

	var dirToRemove string
	commitMsg := fmt.Sprintf("Deleted app '%s'", opts.AppName)
	if opts.Global {
		dirToRemove = appDir
	} else {
		appOverlaysDir := repofs.Join(appDir, store.Default.OverlaysDir)
		overlaysExists := repofs.ExistsOrDie(appOverlaysDir)
		if !overlaysExists {
			appOverlaysDir = appDir
		}

		appProjectDir := repofs.Join(appOverlaysDir, opts.ProjectName)
		overlayExists := repofs.ExistsOrDie(appProjectDir)
		if !overlayExists {
			return fmt.Errorf("application '%s' not found in project '%s'", opts.AppName, opts.ProjectName)
		}

		allOverlays, err := repofs.ReadDir(appOverlaysDir)
		if err != nil {
			return fmt.Errorf("failed to read overlays directory '%s': %w", appOverlaysDir, err)
		}

		if len(allOverlays) == 1 {
			dirToRemove = appDir
		} else {
			commitMsg += fmt.Sprintf(" from project '%s'", opts.ProjectName)
			dirToRemove = appProjectDir
		}
	}

	err = billyUtils.RemoveAll(repofs, dirToRemove)
	if err != nil {
		return fmt.Errorf("failed to delete directory '%s': %w", dirToRemove, err)
	}

	log.Info("committing changes to gitops repo...")
	if _, err = r.Persist(ctx, &git.PushOptions{CommitMsg: commitMsg}); err != nil {
		return fmt.Errorf("failed to push to repo: %w", err)
	}

	return nil
}
