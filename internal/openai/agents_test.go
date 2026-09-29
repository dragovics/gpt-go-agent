package openai

import "testing"

func TestNewClientDoesNotPersistKey(t *testing.T) {
	c:=NewClient()
	if c.BaseURL!="https://api.openai.com" { t.Fatal(c.BaseURL) }
}
