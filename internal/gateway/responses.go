package gateway

import (
	"net/http"
	"strings"
	"time"

	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/telemetry"
)

// handleResponses 是 OpenAI Responses 入站（POST /v1/responses）的 handler 骨架。
// 鉴权/并发/读体走共用 gate，随后解析请求并做模型解析；错误体为 OpenAI 形状
// （与 /v1/chat/completions 一致）。目标链 failover 由 Task 15/16 接入——在
// candidates 解析完成之后，本任务暂以 500 占位收尾。
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rec := telemetry.RequestRecord{ID: telemetryID(), StartedAt: started}
	switches := 0
	defer func() {
		rec.DurationMs = time.Since(started).Milliseconds()
		rec.FailoverCount = switches
		// 请求日志行（spec §10 审计）：只记脱敏元数据；零密钥、零请求体、零 Authorization。
		if s.deps.Log != nil {
			s.deps.Log.Info("request", "request_id", rec.ID, "model", rec.Model,
				"upstream_model", rec.UpstreamModel, "channel", rec.Channel,
				"account", rec.Account, "status", rec.Status,
				"duration_ms", rec.DurationMs, "error_class", rec.ErrorClass,
				"streaming", rec.Streaming, "switches", switches)
		}
		if s.deps.Ring != nil {
			s.deps.Ring.Add(rec)
		}
		if s.deps.OnRecord != nil {
			s.deps.OnRecord(rec)
		}
	}()

	body, release, status, errType, msg, ok := s.gate(r, &rec)
	if release != nil {
		defer release()
	}
	if !ok {
		writeErrOpenAI(w, status, errType, msg)
		return
	}
	req, err := protocol.ParseResponsesRequest(body)
	if err != nil {
		// previous_response_id 等不支持字段由 ParseResponsesRequest 显式拒为 400，
		// 避免静默丢失上下文。
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, err.Error())
		return
	}
	req.Model = stripOneM(req.Model)
	rec.Model, rec.Streaming = req.Model, req.Stream

	cands, err := s.deps.Candidates(req.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErrOpenAI(w, 500, protocol.ErrTypeAPI, "candidates unavailable")
		return
	}
	if len(cands) == 0 {
		// 未登记模型：列出公开模型名便于客户端纠错（错误体仍为 OpenAI 形状）。
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErrOpenAI(w, 404, protocol.ErrTypeNotFound,
			"unknown model "+req.Model+"; models: "+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	// Task 15/16 在此接入 failover。
	rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
	writeErrOpenAI(w, 500, protocol.ErrTypeAPI, "responses handler not wired")
}
