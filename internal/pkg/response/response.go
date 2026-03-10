package response

import (
	"math/rand/v2"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	contextResponseMessageKey = "mx:response:message"
	contextErrorLoggedKey     = "mx:response:error_logged"
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

var methodNotAllowedMessages = []string{
	"这个姿势不太对哦，换个方式试试？(๑•̀ㅂ•́)و✧",
	"方法不被允许，但你的勇气值得肯定 (｀・ω・´)",
	"服务器：这个我不能做啦 (´･_･`)",
	"请求方式不合规，已被协议警察拦下 (҂‾ ▵‾)︻デ═一",
	"这个接口不吃这套，请换种喂法 |_・)",
	"姿势错了，再来一次！(ง •̀_•́)ง",
	"Method 不对，世界线拒绝响应",
	"你敲错门啦，这扇门不认这种敲法 (,,#ﾟДﾟ)",
	"这个请求方式，被服务器无情驳回 ( ´•︵•` )",
	"换个姿势，也许它就愿意了呢 ( •̀ ω •́ )✧",
	"操作方式超出本接口技能树 ｜д•´)!!",
	"你用了不存在的操作，服务器选择装死",
	"方法不被允许，但错误信息被允许出现 Ｃ：。ミ",
	"协议表示：不可以哦 ( >﹏<。)",
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
	abortWithMessage(c, http.StatusBadRequest, message)
}

// Unauthorized sends a 401 error response.
func Unauthorized(c *gin.Context) {
	abortWithMessage(c, http.StatusUnauthorized, "你好像还没登录呢 ((/- -)/")
}

// Forbidden sends a 403 error response.
func Forbidden(c *gin.Context) {
	abortWithMessage(c, http.StatusForbidden, "坏！不给你看")
}

// ForbiddenMsg sends a 403 error response with a custom message.
func ForbiddenMsg(c *gin.Context, message string) {
	abortWithMessage(c, http.StatusForbidden, message)
}

// NotFound sends a 404 error response.
func NotFound(c *gin.Context) {
	msg := "Not Found"
	if len(notFoundMessages) > 0 {
		msg = notFoundMessages[rand.IntN(len(notFoundMessages))]
	}
	abortWithMessage(c, http.StatusNotFound, msg)
}

// NotFoundMsg sends a 404 error with a custom message.
func NotFoundMsg(c *gin.Context, message string) {
	abortWithMessage(c, http.StatusNotFound, message)
}

// MethodNotAllowed sends a 405 error response.
func MethodNotAllowed(c *gin.Context) {
	msg := "Method Not Allowed"
	if len(methodNotAllowedMessages) > 0 {
		msg = methodNotAllowedMessages[rand.IntN(len(methodNotAllowedMessages))]
	}
	abortWithMessage(c, http.StatusMethodNotAllowed, msg)
}

// InternalError sends a 500 error response.
func InternalError(c *gin.Context, err error) {
	message := http.StatusText(http.StatusInternalServerError)
	if err != nil {
		message = err.Error()
		zap.L().Named("HTTPResponse").Error("request failed",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.RequestURI()),
			zap.String("ip", c.ClientIP()),
			zap.Error(err),
		)
		MarkErrorLogged(c)
	}
	abortWithMessage(c, http.StatusInternalServerError, message)
}

// UnprocessableEntity sends a 422 error response.
func UnprocessableEntity(c *gin.Context, message string) {
	abortWithMessage(c, http.StatusUnprocessableEntity, message)
}

// Conflict sends a 409 error response.
func Conflict(c *gin.Context, message string) {
	abortWithMessage(c, http.StatusConflict, message)
}

// TooManyRequests sends a 429 error response.
func TooManyRequests(c *gin.Context, message string) {
	abortWithMessage(c, http.StatusTooManyRequests, message)
}

// SetResponseMessage stores the response message for downstream logging middleware.
func SetResponseMessage(c *gin.Context, message string) {
	if c == nil {
		return
	}
	c.Set(contextResponseMessageKey, message)
}

// ResponseMessage returns the stored response message.
func ResponseMessage(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v, ok := c.Get(contextResponseMessageKey)
	if !ok {
		return ""
	}
	message, _ := v.(string)
	return message
}

// MarkErrorLogged marks the current request as already logged.
func MarkErrorLogged(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(contextErrorLoggedKey, true)
}

// ErrorLogged reports whether the current request already emitted an error log.
func ErrorLogged(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(contextErrorLoggedKey)
	if !ok {
		return false
	}
	logged, _ := v.(bool)
	return logged
}

func abortWithMessage(c *gin.Context, status int, message string) {
	SetResponseMessage(c, message)
	c.AbortWithStatusJSON(status, gin.H{"ok": 0, "code": status, "message": message})
}
