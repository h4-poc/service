package handler

import (
	"github.com/gin-gonic/gin"
)

// TODO: CreateApplicationTemplate godoc
func CreateApplicationTemplate(c *gin.Context) {
	c.JSON(200, gin.H{"message": "Helm template created"})
	return
}
