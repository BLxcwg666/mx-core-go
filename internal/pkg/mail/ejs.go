package mail

import (
	_ "embed"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// Custom email templates are EJS (same format as the original core and the admin preview),
// so they are rendered with the ejs browser build inside goja.
//
//go:embed ejs/ejs.min.js
var ejsSource string

var ejsProgram = sync.OnceValues(func() (*goja.Program, error) {
	return goja.Compile("ejs.min.js", "var module={exports:{}};var exports=module.exports;\n"+ejsSource, false)
})

const ejsRenderTimeout = 3 * time.Second

// RenderEJS renders an EJS template with the given props.
func RenderEJS(tpl string, props map[string]interface{}) (string, error) {
	program, err := ejsProgram()
	if err != nil {
		return "", err
	}
	vm := goja.New()
	timer := time.AfterFunc(ejsRenderTimeout, func() { vm.Interrupt("ejs render timeout") })
	defer timer.Stop()

	if _, err := vm.RunProgram(program); err != nil {
		return "", err
	}
	ejs := vm.Get("module").ToObject(vm).Get("exports").ToObject(vm)
	render, ok := goja.AssertFunction(ejs.Get("render"))
	if !ok {
		return "", fmt.Errorf("ejs.render is unavailable")
	}
	out, err := render(goja.Undefined(), vm.ToValue(tpl), vm.ToValue(props))
	if err != nil {
		return "", err
	}
	return out.String(), nil
}
