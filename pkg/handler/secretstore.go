package handler

import (
	"context"
	"fmt"
	"time"

	esv1beta1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1beta1"
	"github.com/gin-gonic/gin"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/spf13/viper"
	"sigs.k8s.io/yaml"

	"github.com/squidflow/service/pkg/application/repowriter"
	"github.com/squidflow/service/pkg/fs"
	"github.com/squidflow/service/pkg/git"
	"github.com/squidflow/service/pkg/log"
	"github.com/squidflow/service/pkg/store"
	"github.com/squidflow/service/pkg/types"
)

func SecretStoreCreate(c *gin.Context) {
	var req types.SecretStoreCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": fmt.Sprintf("Invalid request: %v", err)})
		return
	}

	want := esv1beta1.SecretStore{}
	err := yaml.Unmarshal([]byte(req.SecretStoreYaml), &want)
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to unmarshal SecretStore: %v", err)})
		return
	}

	if want.Spec.Provider == nil {
		c.JSON(400, gin.H{"error": "Provider configuration is required"})
		return
	}

	if want.Spec.Provider.Vault == nil {
		c.JSON(400, gin.H{"error": "Only Vault provider is supported"})
		return
	}

	if want.Annotations != nil && want.Annotations["squidflow.github.io/id"] != "" {
		c.JSON(400, gin.H{"error": "id not allow set via client"})
		return
	}
	if want.Annotations == nil {
		want.Annotations = make(map[string]string)
	}
	want.Annotations["squidflow.github.io/last-synced"] = time.Now().Format(time.RFC3339)
	want.Annotations["squidflow.github.io/created-at"] = time.Now().Format(time.RFC3339)
	want.Annotations["squidflow.github.io/updated-at"] = time.Now().Format(time.RFC3339)
	want.Annotations["squidflow.github.io/id"] = getNewId()

	log.G().WithFields(log.Fields{
		"id": want.Annotations["squidflow.github.io/id"],
	}).Debug("generated id for secret store")

	log.G().WithFields(log.Fields{
		"name":          want.Name,
		"namespace":     want.Namespace,
		"annotations":   want.Annotations,
		"vault_auth":    want.Spec.Provider.Vault.Auth,
		"vault_server":  want.Spec.Provider.Vault.Server,
		"vault_path":    want.Spec.Provider.Vault.Path,
		"vault_version": want.Spec.Provider.Vault.Version,
	}).Debug("Creating SecretStore with Vault provider")

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

	var nativeRepoWrite = repowriter.NativeRepoTarget{}

	if err := nativeRepoWrite.WriteSecretStore2Repo(context.Background(), &want, cloneOpts, false); err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to create external secret: %v", err)})
		return
	}

	c.JSON(201, types.SecretStoreCreateResponse{
		Name:    want.Name,
		ID:      want.Annotations["squidflow.github.io/id"],
		Success: true,
		Message: "SecretStore created successfully",
	})
}

func SecretStoreDelete(c *gin.Context) {
	secretStoreID := c.Param("id")
	if secretStoreID == "" {
		c.JSON(400, gin.H{"error": "SecretStore ID is required"})
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

	var nativeRepoWrite = repowriter.NativeRepoTarget{}

	if err := nativeRepoWrite.RunDeleteSecretStore(context.Background(), secretStoreID, &types.SecretStoreDeleteOptions{
		CloneOpts: cloneOpts,
	}); err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to delete secret store: %v", err)})
		return
	}

	c.JSON(200, types.DeleteSecretStoreResponse{
		Success: true,
		Message: "secret store deleted successfully",
	})
}

func SecretStoreDescribe(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(400, gin.H{"error": "SecretStore ID is required"})
		return
	}

	log.G().WithField("id", id).Debug("describe secret store")

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

	var nativeRepoWrite = repowriter.NativeRepoTarget{}
	secretStore, err := nativeRepoWrite.GetSecretStoreFromRepo(context.Background(), &types.SecretStoreGetOptions{
		CloneOpts: cloneOpts,
		ID:        id,
	})
	if err != nil {
		log.G().Errorf("Failed to get secret store: %v", err)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to get secret store: %v", err)})
		return
	}

	if secretStore == nil {
		c.JSON(404, gin.H{"error": "secret store not found"})
		return
	}

	c.JSON(200, types.DescribeSecretStoreResponse{
		Success: true,
		Item:    *secretStore,
		Message: "secret store retrieved successfully",
	})
}

// SecretStoreList returns a list of secret stores
func SecretStoreList(c *gin.Context) {
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

	var nativeRepoWrite = repowriter.NativeRepoTarget{}
	secretStores, err := nativeRepoWrite.RunListSecretStore(context.Background(), &types.SecretStoreListOptions{
		CloneOpts: cloneOpts,
	})
	if err != nil {
		log.G().Errorf("Failed to list secret stores: %v", err)
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to list secret stores: %v", err)})
		return
	}

	c.JSON(200, types.ListSecretStoreResponse{
		Success: true,
		Total:   len(secretStores),
		Items:   secretStores,
		Message: "secret stores retrieved successfully",
	})
}

func SecretStoreUpdate(c *gin.Context) {
	secretStoreID := c.Param("id")
	if secretStoreID == "" {
		c.JSON(400, gin.H{"error": "secret store ID is required"})
		return
	}

	var req types.SecretStoreUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": fmt.Sprintf("Invalid request: %v", err)})
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

	_, repofs, err := prepareRepo(context.Background(), cloneOpts, "")
	if err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to prepare repo: %v", err)})
		return
	}

	secretStorePath := repofs.Join(
		store.Default.BootsrtrapDir,
		store.Default.ClusterResourcesDir,
		store.Default.ClusterContextName,
		fmt.Sprintf("ss-%s.yaml", secretStoreID),
	)

	secretStore := &esv1beta1.SecretStore{}
	if err := repofs.ReadYamls(secretStorePath, secretStore); err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to read secret store: %v", err)})
		return
	}

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

	var nativeRepoWrite = repowriter.NativeRepoTarget{}
	if err := nativeRepoWrite.WriteSecretStore2Repo(context.Background(), secretStore, cloneOpts, true); err != nil {
		c.JSON(500, gin.H{"error": fmt.Sprintf("Failed to write secret store to repo: %v", err)})
		return
	}

	c.JSON(200, types.SecretStoreUpdateResponse{
		Item: types.SecretStoreDetail{
			ID:       secretStore.Annotations["squidflow.github.io/id"],
			Name:     secretStore.Name,
			Provider: "vault",
			Type:     "SecretStore",

			Status:      "Active",
			Path:        *secretStore.Spec.Provider.Vault.Path,
			LastSynced:  secretStore.Annotations["squidflow.github.io/last-synced"],
			CreatedAt:   secretStore.Annotations["squidflow.github.io/created-at"],
			LastUpdated: secretStore.Annotations["squidflow.github.io/updated-at"],
			Health: types.SecretStoreHealth{
				Status:  "Healthy",
				Message: "Secret store updated successfully",
			},
		},
		Success: true,
		Message: "secret store updated successfully",
	})
}
