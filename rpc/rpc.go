package rpc

import (
	"net/http"

	"risk_control/tools"
	"risk_control/workflow"
)

type Risk struct {
	Eng *workflow.RiskEngine
}

type Args struct {
}

type Result struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
	Error   error  `json:"error"`
	Data    any    `json:"data"`
}

func (c *Risk) Health(r *http.Request, args *Args, result *Result) error {
	result.Message = "OK"
	result.Status = 200
	result.Error = nil
	return nil
}

func (c *Risk) Screen(r *http.Request, req *tools.ScreeningRequest, result *Result) error {
	invokeCtx, _ := workflow.WithRunTrace(r.Context())
	res, err := c.Eng.EvaluateScreeningRequest(invokeCtx, *req, workflow.InvokeScreeningOptions()...)
	if err != nil {
		result.Message = err.Error()
		result.Status = 500
		result.Error = err
		return err
	}
	result.Message = "OK"
	result.Status = 200
	result.Error = nil
	result.Data = res
	return nil
}
