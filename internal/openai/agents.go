package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type Client struct { BaseURL string; APIKey string; HTTP *http.Client }
type Session struct {
	ID string `json:"id"
	Environment struct {
		ID string `json:"id"
		RemoteURL string `json:"remote_url"
	} `json:"environment"
}

func NewClient() *Client {
	return &Client{BaseURL:"https://api.openai.com", APIKey:os.Getenv("OPENAI_API_KEY"), HTTP:&http.Client{Timeout:60*time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	if c.APIKey=="" { return fmt.Errorf("OPENAI_API_KEY is not set") }
	var r *bytes.Reader
	if body==nil { r=bytes.NewReader(nil) } else { b,err:=json.Marshal(body); if err!=nil{return err}; r=bytes.NewReader(b) }
	req,err:=http.NewRequestWithContext(ctx,method,c.BaseURL+path,r); if err!=nil{return err}
	req.Header.Set("Authorization","Bearer "+c.APIKey)
	req.Header.Set("Content-Type","application/json")
	req.Header.Set("OpenAI-Beta","agents=v1")
	resp,err:=c.HTTP.Do(req); if err!=nil{return err}; defer resp.Body.Close()
	if resp.StatusCode<200 || resp.StatusCode>=300 { return fmt.Errorf("agents api: %s",resp.Status) }
	if out!=nil { return json.NewDecoder(resp.Body).Decode(out) }
	return nil
}

type sessionAgent struct { Model string `json:"model"`; Instructions string `json:"instructions,omitempty"` }
type createSessionRequest struct {
	Agent sessionAgent `json:"agent"`
	Environment map[string]any `json:"environment"`
	Input []map[string]any `json:"input,omitempty"`
}

func (c *Client) CreateSelfHostedSession(ctx context.Context, model, instructions, workspace string, input string) (Session,error) {
	req:=createSessionRequest{Agent:sessionAgent{Model:model,Instructions:instructions},Environment:map[string]any{"type":"self_hosted","workspace_directory":workspace}}
	if input!="" { req.Input=[]map[string]any{{"role":"user","content":[]map[string]any{{"type":"input_text","text":input}}}} }
	var s Session
	err:=c.do(ctx,http.MethodPost,"/v1/agents/sessions",req,&s)
	return s,err
}

func (c *Client) RetrieveSession(ctx context.Context,id string)(Session,error){var s Session;err:=c.do(ctx,http.MethodGet,"/v1/agents/sessions/"+id,nil,&s);return s,err}

func (c *Client) SubmitInput(ctx context.Context,id,text string) error {
	body:=map[string]any{"input":[]map[string]any{{"role":"user","content":[]map[string]any{{"type":"input_text","text":text}}}}}
	return c.do(ctx,http.MethodPost,"/v1/agents/sessions/"+id+"/turns",body,nil)
}
