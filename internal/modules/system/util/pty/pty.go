package pty

import "github.com/gin-gonic/gin"

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/pty", authMW)
	g.GET("/record", h.records)
}

func (h *Handler) records(c *gin.Context) {
	c.JSON(200, gin.H{
		"data": ListSessions(),
	})
}
