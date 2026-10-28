// asprobe 临时诊断（不入库）：测 DeepSeek anthropic 兼容端点对 thinking 的
// 契约——thinking 开关 + 历史 thinking 块的不同组合。密钥仅存内存不打印。
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"AgentSwitch/internal/credential"
)

type probeFile struct {
	Key      string `json:"key"`
	Channels []struct {
		Name, Protocol, Base, Ct string
	} `json:"channels"`
}

func post(name, base, key, body string) {
	ep, err := providerAnthropicEndpoint(base)
	if err != nil {
		fmt.Println("endpoint:", err)
		return
	}
	req, _ := http.NewRequest(http.MethodPost, ep, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Accept", "text/event-stream")
	c := &http.Client{Timeout: 60 * time.Second}
	start := time.Now()
	res, err := c.Do(req)
	if err != nil {
		fmt.Printf("[%s] err %v\n", name, err)
		return
	}
	defer res.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	fmt.Printf("[%s] status=%d ttfb=%.1fs body=%s\n", name, res.StatusCode, time.Since(start).Seconds(), firstLine(snippet))
}

func firstLine(b []byte) string {
	s := string(b)
	if len(s) > 220 {
		s = s[:220] + "…"
	}
	return s
}

func main() {
	var pf probeFile
	raw, _ := os.ReadFile("/tmp/as_probe.json")
	if err := json.Unmarshal(raw, &pf); err != nil {
		panic(err)
	}
	mk, err := credential.LoadMasterKey("/home/zhangcao/.config/AgentSwitch/master.key")
	if err != nil {
		panic(err)
	}
	var base, key string
	for _, ch := range pf.Channels {
		if ch.Name == "ds" {
			ct, _ := base64.StdEncoding.DecodeString(ch.Ct)
			k, err := mk.Open(ct)
			if err != nil {
				panic(err)
			}
			base, key = ch.Base, string(k)
		}
	}
	if key == "" {
		panic("ds channel not found")
	}
	const text = `{"role":"assistant","content":[{"type":"text","text":"好的"}]}`
	const withThinking = `{"role":"assistant","content":[{"type":"thinking","thinking":"思考过程","signature":"sig-test"},{"type":"text","text":"好的"}]}`
	user := `{"role":"user","content":"hi"}`
	th := `"thinking":{"type":"enabled","budget_tokens":1024},`
	toolUse := `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"x"}}]}`
	toolUseThink := `{"role":"assistant","content":[{"type":"thinking","thinking":"想一想","signature":"sig-test"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"x"}}]}`
	toolResult := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"ok"}]}]}`
	post("E thinking开+tool_use无thinking", base, key,
		`{"model":"deepseek-flash","max_tokens":64,"stream":true,`+th+`"messages":[`+toolUse+`,`+toolResult+`]}`)
	post("F thinking开+tool_use有thinking", base, key,
		`{"model":"deepseek-flash","max_tokens":64,"stream":true,`+th+`"messages":[`+toolUseThink+`,`+toolResult+`]}`)
	toolUseThinkNoSig := `{"role":"assistant","content":[{"type":"thinking","thinking":"想一想"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"x"}}]}`
	post("G thinking开+thinking块无signature", base, key,
		`{"model":"deepseek-flash","max_tokens":64,"stream":true,`+th+`"messages":[`+toolUseThinkNoSig+`,`+toolResult+`]}`)
	post("A thinking开+有thinking块", base, key,
		`{"model":"deepseek-flash","max_tokens":64,"stream":true,`+th+`"messages":[`+withThinking+`,`+user+`]}`)
	post("B thinking开+无thinking块", base, key,
		`{"model":"deepseek-flash","max_tokens":64,"stream":true,`+th+`"messages":[`+text+`,`+user+`]}`)
	post("C thinking关+有thinking块", base, key,
		`{"model":"deepseek-flash","max_tokens":64,"stream":true,"messages":[`+withThinking+`,`+user+`]}`)
	post("D thinking关+无thinking块", base, key,
		`{"model":"deepseek-flash","max_tokens":64,"stream":true,"messages":[`+text+`,`+user+`]}`)
}
