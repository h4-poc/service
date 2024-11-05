package handler

import (
	"github.com/gin-gonic/gin"
)

func DryRunArgoApplications(c *gin.Context) {
	c.JSON(200, "response")
}
