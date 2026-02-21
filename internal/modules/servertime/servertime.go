package servertime

import (
	"time"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes mounts server clock sync endpoint.
func RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/server-time", func(c *gin.Context) {
		t2 := time.Now().UnixMilli()
		c.JSON(200, gin.H{
			"t2": t2,
			"t3": time.Now().UnixMilli(),
		})
	})
}
