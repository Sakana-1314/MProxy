package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *API) getSettings(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"proxy_listen": a.proxyAddr,
	})
}
