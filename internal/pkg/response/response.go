package response

import (
	"math/rand/v2"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
)

var notFoundMessages = []string{
	"真不巧，内容走丢了 o(╥﹏╥)o",
	"电波无法到达 ωω",
	"数据..不小心丢失了啦 π_π",
	"404, 这也不是我的错啦 (๐•̆ ·̭ •̆๐)",
	"嘿，这里空空如也，不如别处走走？",

	"这里什么都没有，连 bug 都懒得来 (￣▽￣)",
	"请求已发射，但命中了虚空 ଘ(੭ˊ꒳ˋ)੭✧",
	"服务器翻了翻口袋：真的没有啦 ( ´･ω･`)",
	"你要找的东西，可能在平行宇宙 (◞‸◟ )",
	"前方是未探索区域，请谨慎前行 (ง •̀_•́)ง",
	"404：世界线发生了轻微偏移 |д･)っ",
	"内容不存在，但梦想还是要有的（？）",
	"这个页面可能去度假了，还没回来 (●′ω`●)️",
	"访问到了不存在的存在，这很哲学 ∠(´д｀)",
	"这里是空的，但你的好奇心不是 (ฅ´ω`ฅ)",
}

// Pagination metadata returned with paginated responses.
type Pagination struct {
	Total       int64 `json:"total"`
	CurrentPage int   `json:"current_page"`
	TotalPage   int   `json:"total_page"`
	Size        int   `json:"size"`
	HasNextPage bool  `json:"has_next_page"`
}

// pagedResponse is the envelope for paginated list responses.
type pagedResponse struct {
	Data       interface{} `json:"data"`
	Pagination Pagination  `json:"pagination"`
}

// OK sends a 200 response. Arrays/slices are wrapped in {data: [...]}.
func OK(c *gin.Context, data interface{}) {
	if data != nil {
		v := reflect.ValueOf(data)
		if v.Kind() == reflect.Slice {
			c.JSON(http.StatusOK, gin.H{"data": data})
			return
		}
	}
	c.JSON(http.StatusOK, data)
}

// Paged sends a paginated response.
func Paged(c *gin.Context, data interface{}, pagination Pagination) {
	c.JSON(http.StatusOK, pagedResponse{
		Data:       data,
		Pagination: pagination,
	})
}

// Created sends a 201 response.
func Created(c *gin.Context, data interface{}) {
	c.JSON(http.StatusCreated, data)
}

// NoContent sends a 204 response.
func NoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// BadRequest sends a 400 error response.
func BadRequest(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"ok": 0, "code": http.StatusBadRequest, "message": message})
}

// Unauthorized sends a 401 error response.
func Unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": 0, "code": http.StatusUnauthorized, "message": "你好像还没登录呢 ((/- -)/"})
}

// Forbidden sends a 403 error response.
func Forbidden(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"ok": 0, "code": http.StatusForbidden, "message": "坏！不给你看"})
}

// ForbiddenMsg sends a 403 error response with a custom message.
func ForbiddenMsg(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"ok": 0, "code": http.StatusForbidden, "message": message})
}

// NotFound sends a 404 error response.
func NotFound(c *gin.Context) {
	msg := "Not Found"
	if len(notFoundMessages) > 0 {
		msg = notFoundMessages[rand.IntN(len(notFoundMessages))]
	}
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"ok": 0, "code": http.StatusNotFound, "message": msg})
}

// NotFoundMsg sends a 404 error with a custom message.
func NotFoundMsg(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"ok": 0, "code": http.StatusNotFound, "message": message})
}

// InternalError sends a 500 error response.
func InternalError(c *gin.Context, err error) {
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"ok": 0, "code": http.StatusInternalServerError, "message": err.Error()})
}

// UnprocessableEntity sends a 422 error response.
func UnprocessableEntity(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{"ok": 0, "code": http.StatusUnprocessableEntity, "message": message})
}

// Conflict sends a 409 error response.
func Conflict(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusConflict, gin.H{"ok": 0, "code": http.StatusConflict, "message": message})
}
