package handler

import (
	"github.com/gin-gonic/gin"
)

// TODO: ListApplicationTemplate godoc
func ListApplicationTemplate(c *gin.Context) {
	var helmTemplateList = []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Url         string `json:"url"`
		Maintainers []struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"maintainers"`
	}{
		{
			Name:        "h4-loki",
			Description: "Loki is a horizontally scalable, highly available, multi-tenant log aggregation system inspired by Prometheus. It is designed to be very cost effective and easy to operate. It is the best solution for large-scale microservices based systems.",
			Url:         "https://github.com/h4-poc/manifest/blob/main/loki/values.yaml",
			Maintainers: []struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			}{
				{
					Name:  "h4-loki",
					Email: "h4-loki@h4.com",
				},
			},
		},
		{
			Name:        "h4-logging-operator",
			Description: "Logging operator is a tool for managing logging resources in Kubernetes. It is designed to be very cost effective and easy to operate. It is the best solution for large-scale microservices based systems.",
			Url:         "https://github.com/h4-poc/manifest/blob/main/logging-operator/values.yaml",
			Maintainers: []struct {
				Name  string `json:"name"`
				Email string `json:"email"`
			}{
				{
					Name:  "h4-logging-operator",
					Email: "h4-logging-operator@h4.com",
				},
			},
		},
	}
	c.JSON(200, helmTemplateList)
	return
}
