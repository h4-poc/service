package handler

import (
	"github.com/gin-gonic/gin"
)

func UpdateArgoApplication(c *gin.Context) {
	c.JSON(200, gin.H{"message": "Application updated"})
}
