package search

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mx-space/core/internal/middleware"
	"github.com/mx-space/core/internal/pkg/response"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup, authMW gin.HandlerFunc) {
	g := rg.Group("/search")
	g.GET("", h.search)
	g.GET("/type/:type", h.searchByType)
	g.POST("/index", authMW, h.reindex)
	g.POST("/meili/push", authMW, h.reindex)

	// Algolia-compatible stubs (reuse MeiliSearch implementation)
	g.GET("/algolia", h.search)
	g.POST("/algolia/push", authMW, h.reindex)
	g.GET("/algolia/import-json", authMW, h.algoliaExportJSON)
}

func (h *Handler) search(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		response.BadRequest(c, "q is required")
		return
	}
	results, servedBy, err := h.svc.Search(q)
	c.Header("x-mx-served-by", servedBy)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	if results == nil {
		results = []SearchResult{}
	}
	response.OK(c, gin.H{"data": results, "query": q})
}

func (h *Handler) reindex(c *gin.Context) {
	go h.svc.IndexAll()
	response.OK(c, gin.H{"message": "indexing started"})
}

func (h *Handler) algoliaExportJSON(c *gin.Context) {
	docs, err := h.svc.GetAllDocuments()
	if err != nil {
		response.InternalError(c, err)
		return
	}
	data, err := json.Marshal(docs)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	filename := fmt.Sprintf("algolia-export-%s.json", time.Now().Format("20060102_150405"))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(200, "application/json", data)
}

func (h *Handler) searchByType(c *gin.Context) {
	c.Header("x-mx-served-by", servedByMySQL)

	docType := c.Param("type")
	keyword := c.Query("keyword")
	if keyword == "" {
		keyword = c.Query("q")
	}

	page := 1
	size := 20
	if v := c.Query("page"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			page = i
		}
	}
	if v := c.Query("size"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			size = i
		}
	}

	results, pag, err := h.svc.SearchByType(docType, keyword, page, size, middleware.IsAuthenticated(c))
	if err != nil {
		response.InternalError(c, err)
		return
	}
	response.Paged(c, results, pag)
}
