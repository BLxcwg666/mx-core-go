package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts socket.io and stats endpoints.
func RegisterRoutes(rg *gin.RouterGroup, hub *Hub) {
	handler := gin.WrapH(hub.Handler())
	rg.Any("/socket.io", handler)
	rg.Any("/socket.io/*any", handler)

	rg.GET("/gateway/stats", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"public": hub.ClientCount(RoomPublic),
			"admin":  hub.ClientCount(RoomAdmin),
			"total":  hub.ClientCount(""),
		})
	})
}
