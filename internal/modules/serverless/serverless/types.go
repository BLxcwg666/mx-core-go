package serverless

import (
	"errors"
	"time"
)

const serverlessExecutionTimeout = 30 * time.Second
const serverlessCacheKeyPrefix = "mx:serverless:storage:cache:"
const serverlessOnlineAssetBaseURL = "https://cdn.jsdelivr.net/gh/mx-space/assets@master/"

type compiledSnippet struct {
	UpdatedAt time.Time
	Code      string
}

type runtimeResponseMeta struct {
	StatusCode  int
	ContentType string
	Sent        bool
	SentData    interface{}
	SentHasData bool
}

type runtimeContext struct {
	Req             map[string]interface{}
	Query           map[string]interface{}
	Headers         map[string]string
	Params          map[string]interface{}
	Method          string
	Path            string
	URL             string
	IP              string
	Body            interface{}
	IsAuthenticated bool
	Secret          map[string]interface{}
	Model           map[string]interface{}
}

type executorResult struct {
	data    interface{}
	hasData bool
	meta    runtimeResponseMeta
}

type runtimeExecError struct {
	Status  int
	Message string
}

func (e *runtimeExecError) Error() string { return e.Message }

func asRuntimeExecError(err error, target **runtimeExecError) bool {
	return errors.As(err, target)
}

type axiosRequestError struct {
	Message  string
	Response map[string]interface{}
}

func (e *axiosRequestError) Error() string { return e.Message }
