// responses_stream_items.go 是 Responses 流式转换的条目装配侧：文本/推理/
// 工具调用条目的增量状态与收尾产出（结构体与方法定义在 chatToResponsesStream
// 上，SSE 管道见 responses_stream.go）。
package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// responsesTextState 记录流式文本消息项的进度：一旦输出过文本就开启一个
// message 项，收口时补齐 done 事件。
type responsesTextState struct {
	index int
	id    string
	text  strings.Builder
	open  bool
	done  bool
	item  map[string]any // 收口后的完成态 item，供最终 output 聚合使用
}

// responsesReasoningState 记录流式 reasoning summary 项的进度：
// 首个 reasoning 增量开启一项，正文开始或收口时补齐 done 事件。
type responsesReasoningState struct {
	index int
	id    string
	text  strings.Builder
	open  bool
	done  bool
	item  map[string]any // 收口后的完成态 item，供最终 output 聚合使用
}

// responseOutputItem 是收口阶段的一个完成态 output 项及其 output_index。
// reasoning、文本与工具都汇入同一集合，统一按 index 升序后写入
// response.completed / response.failed 的 output。
type responseOutputItem struct {
	index int
	item  map[string]any
}

// responsesToolState 记录一个流式工具调用的进度。
// kind/itemType/idPrefix 的零值即 function / function_call / fc_；工具名首次
// 到达后按工具上下文解析一次真实类型（custom/tool_search）。
type responsesToolState struct {
	index        int
	callID       string
	name         string
	args         strings.Builder
	open         bool
	done         bool
	kind         responseToolKind
	itemType     string
	idPrefix     string
	kindResolved bool
	completed    map[string]any
}

// sortedOutputItems 按 output_index 升序整理最终 output，返回非 nil 数组。
func sortedOutputItems(items []responseOutputItem) []map[string]any {
	sort.SliceStable(items, func(i, j int) bool { return items[i].index < items[j].index })
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.item)
	}
	return out
}

// pushText 追加文本增量；首个增量触发 message 项与 content_part 的开启事件。
func (t *chatToResponsesStream) pushText(delta string) {
	// 文本项已收口（例如 reasoning 插入后正文再次到达）：迟到增量必须丢弃，
	// 否则会发出 completed 快照中不存在的内容。
	if t.text.done {
		return
	}
	t.ensureStarted()
	if !t.text.open {
		t.text.index = t.nextIndex
		t.nextIndex++
		t.text.id = t.respID + "_msg"
		t.text.open = true
		t.out.Write(responsesSSEEvent("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": t.text.index,
			"item": map[string]any{"id": t.text.id, "type": "message", "status": "in_progress",
				"role": "assistant", "content": []any{}},
		}))
		t.out.Write(responsesSSEEvent("response.content_part.added", map[string]any{
			"type": "response.content_part.added", "item_id": t.text.id,
			"output_index": t.text.index, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		}))
	}
	t.text.text.WriteString(delta)
	t.out.Write(responsesSSEEvent("response.output_text.delta", map[string]any{
		"type": "response.output_text.delta", "item_id": t.text.id,
		"output_index": t.text.index, "content_index": 0, "delta": delta,
	}))
}

// closeText 收口文本项并返回其完成态 item；未开启时返回 nil。
// 若此前已收口（reasoning 插入时），直接返回留存的 item，供最终 output 聚合，
// 但不再重复下发 done 事件。
func (t *chatToResponsesStream) closeText() []responseOutputItem {
	if !t.text.open {
		return nil
	}
	if t.text.done {
		if t.text.item == nil {
			return nil
		}
		return []responseOutputItem{{index: t.text.index, item: t.text.item}}
	}
	t.text.done = true
	text := t.text.text.String()
	item := map[string]any{
		"id": t.text.id, "type": "message", "status": "completed", "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
	}
	t.out.Write(responsesSSEEvent("response.output_text.done", map[string]any{
		"type": "response.output_text.done", "item_id": t.text.id,
		"output_index": t.text.index, "content_index": 0, "text": text,
	}))
	t.out.Write(responsesSSEEvent("response.content_part.done", map[string]any{
		"type": "response.content_part.done", "item_id": t.text.id,
		"output_index": t.text.index, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
	}))
	t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": t.text.index, "item": item,
	}))
	t.text.item = item
	return []responseOutputItem{{index: t.text.index, item: item}}
}

// pushReasoning 追加一段 reasoning 摘要；首个增量开启 reasoning 项，
// 若此时文本项仍在流式中则先收口它（两段 item 各自独立收口）。
func (t *chatToResponsesStream) pushReasoning(delta string) {
	// reasoning 项已收口（正文开始之后 reasoning 再次到达）：迟到增量必须丢弃，
	// 保持流事件与 completed 快照一致。
	if t.reasoning.done {
		return
	}
	t.ensureStarted()
	if !t.reasoning.open {
		t.closeText()
		t.reasoning.index = t.nextIndex
		t.nextIndex++
		t.reasoning.id = "rs_" + t.respID
		t.reasoning.open = true
		t.out.Write(responsesSSEEvent("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": t.reasoning.index,
			"item": map[string]any{"id": t.reasoning.id, "type": "reasoning", "status": "in_progress", "summary": []any{}},
		}))
		t.out.Write(responsesSSEEvent("response.reasoning_summary_part.added", map[string]any{
			"type": "response.reasoning_summary_part.added", "item_id": t.reasoning.id,
			"output_index": t.reasoning.index, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": ""},
		}))
	}
	t.reasoning.text.WriteString(delta)
	t.out.Write(responsesSSEEvent("response.reasoning_summary_text.delta", map[string]any{
		"type": "response.reasoning_summary_text.delta", "item_id": t.reasoning.id,
		"output_index": t.reasoning.index, "summary_index": 0, "delta": delta,
	}))
}

// closeReasoning 收口 reasoning 项并返回其完成态 item；未开启时返回 nil。
// 若此前已收口（正文开始时），直接返回留存的 item，供最终 output 聚合，
// 但不再重复下发 done 事件。完成态 item 不带 status 字段（与 Responses 一致）。
func (t *chatToResponsesStream) closeReasoning() []responseOutputItem {
	if !t.reasoning.open {
		return nil
	}
	if t.reasoning.done {
		if t.reasoning.item == nil {
			return nil
		}
		return []responseOutputItem{{index: t.reasoning.index, item: t.reasoning.item}}
	}
	t.reasoning.done = true
	text := t.reasoning.text.String()
	item := map[string]any{
		"id": t.reasoning.id, "type": "reasoning",
		"summary": []map[string]any{{"type": "summary_text", "text": text}},
	}
	t.out.Write(responsesSSEEvent("response.reasoning_summary_text.done", map[string]any{
		"type": "response.reasoning_summary_text.done", "item_id": t.reasoning.id,
		"output_index": t.reasoning.index, "summary_index": 0, "text": text,
	}))
	t.out.Write(responsesSSEEvent("response.reasoning_summary_part.done", map[string]any{
		"type": "response.reasoning_summary_part.done", "item_id": t.reasoning.id,
		"output_index": t.reasoning.index, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": text},
	}))
	t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": t.reasoning.index, "item": item,
	}))
	t.reasoning.item = item
	return []responseOutputItem{{index: t.reasoning.index, item: item}}
}

// pushToolDelta 累积一个工具调用分片。与 cc-switch 一致：必须等 call_id 与 name
// 都到齐才发 output_item.added（部分上游把 name 与 id 分在不同分片）；此前到达的
// 参数先缓冲，added 时一次性补发。
func (t *chatToResponsesStream) pushToolDelta(index int, id, name, args string) {
	t.ensureStarted()
	if t.tools == nil {
		t.tools = map[int]*responsesToolState{}
	}
	st := t.tools[index]
	if st == nil {
		st = &responsesToolState{index: t.nextIndex, idPrefix: "fc_", itemType: "function_call"}
		t.nextIndex++
		t.tools[index] = st
		t.toolOrder = append(t.toolOrder, index)
	}
	if id != "" {
		st.callID = id
	}
	if name != "" {
		st.name = name
	}
	// 工具名到齐后按工具上下文解析一次真实类型，决定 added/done 的 item 形状
	// 与 id 前缀；未登记的工具按普通 function 透传。
	if st.name != "" && !st.kindResolved {
		st.kindResolved = true
		st.kind = t.toolSpec(st.name).Kind
		switch st.kind {
		case responseToolCustom:
			st.itemType, st.idPrefix = "custom_tool_call", "ctc_"
		case responseToolSearch:
			st.itemType, st.idPrefix = "tool_search_call", "ts_"
		}
	}
	if args != "" {
		st.args.WriteString(args)
	}

	if !st.open && st.callID != "" && st.name != "" {
		t.openTool(st)
		return
	}
	if st.open && args != "" && streamsToolArgumentDeltas(st.kind) {
		t.out.Write(responsesSSEEvent("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": st.idPrefix + st.callID,
			"output_index": st.index, "delta": args,
		}))
	}
}

// toolSpec 解析 chat 名对应的 Responses 工具语义；未登记的工具按普通 function 透传。
func (t *chatToResponsesStream) toolSpec(name string) responseToolSpec {
	if t.tc != nil {
		if spec, ok := t.tc.Spec(name); ok {
			return spec
		}
	}
	return responseToolSpec{Kind: responseToolFunction, Name: name}
}

// openTool 发出 output_item.added；参数若在 call_id/name 到齐前已缓冲，此处一次性补发。
// 工具名按工具上下文还原（namespace 子函数用短名 + namespace），保证 added 与
// done 两个 item 的命名一致。
func (t *chatToResponsesStream) openTool(st *responsesToolState) {
	st.open = true
	spec := t.toolSpec(st.name)
	name := spec.Name
	if name == "" {
		name = st.name
	}
	item := map[string]any{
		"id": st.idPrefix + st.callID, "type": st.itemType, "status": "in_progress",
		"call_id": st.callID,
	}
	if st.kind == responseToolSearch {
		item["execution"] = "client"
		item["arguments"] = map[string]any{}
	} else {
		item["name"] = name
		if spec.Namespace != "" {
			item["namespace"] = spec.Namespace
		}
		if st.kind == responseToolCustom {
			item["input"] = ""
		} else {
			item["arguments"] = ""
		}
	}
	t.out.Write(responsesSSEEvent("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": st.index, "item": item,
	}))
	if st.args.Len() > 0 && streamsToolArgumentDeltas(st.kind) {
		t.out.Write(responsesSSEEvent("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": st.idPrefix + st.callID,
			"output_index": st.index, "delta": st.args.String(),
		}))
	}
}

// closeTools 依次收口工具调用并返回完成态 item。
// 未拿到工具名的调用无法还原语义，直接丢弃；id 缺失时合成非空 call_id，
// 并补发 added，保证客户端看到的事件序列完整（不会出现只有 done 的孤儿 item）。
func (t *chatToResponsesStream) closeTools() []responseOutputItem {
	var output []responseOutputItem
	for _, idx := range t.toolOrder {
		st := t.tools[idx]
		if st == nil || st.done || st.name == "" {
			continue
		}
		st.done = true
		callID := st.callID
		if callID == "" {
			callID = fmt.Sprintf("call_%d", st.index)
			st.callID = callID
		}
		if !st.open {
			t.openTool(st)
		}
		spec := t.toolSpec(st.name)
		switch st.kind {
		case responseToolCustom:
			// custom 不流式下发输入增量，收口时解包 {"input":...} 后一次性补发。
			input := customToolInput(st.args.String())
			item := map[string]any{
				"id": st.idPrefix + callID, "type": st.itemType, "status": "completed",
				"call_id": callID, "name": spec.Name, "input": input,
			}
			if input != "" {
				t.out.Write(responsesSSEEvent("response.custom_tool_call_input.delta", map[string]any{
					"type": "response.custom_tool_call_input.delta", "item_id": st.idPrefix + callID,
					"output_index": st.index, "delta": input,
				}))
			}
			t.out.Write(responsesSSEEvent("response.custom_tool_call_input.done", map[string]any{
				"type": "response.custom_tool_call_input.done", "item_id": st.idPrefix + callID,
				"output_index": st.index, "input": input,
			}))
			t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": st.index, "item": item,
			}))
			st.completed = item
			output = append(output, responseOutputItem{index: st.index, item: item})
		case responseToolSearch:
			item := map[string]any{
				"id": st.idPrefix + callID, "type": st.itemType, "status": "completed",
				"call_id": callID, "execution": "client",
				"arguments": parseToolArgumentsObject(st.args.String()),
			}
			t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": st.index, "item": item,
			}))
			st.completed = item
			output = append(output, responseOutputItem{index: st.index, item: item})
		default:
			// function（含未登记工具与 namespace 子函数的透传）收口。
			args := st.args.String()
			if args == "" {
				args = "{}"
			}
			item := map[string]any{
				"id": st.idPrefix + callID, "type": st.itemType, "status": "completed",
				"call_id": callID, "name": spec.Name, "arguments": args,
			}
			if spec.Namespace != "" {
				item["namespace"] = spec.Namespace
			}
			t.out.Write(responsesSSEEvent("response.function_call_arguments.done", map[string]any{
				"type": "response.function_call_arguments.done", "item_id": st.idPrefix + callID,
				"output_index": st.index, "arguments": args,
			}))
			t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": st.index, "item": item,
			}))
			st.completed = item
			output = append(output, responseOutputItem{index: st.index, item: item})
		}
	}
	return output
}

// completedToolItems 返回已收口的工具 item；未完成的不下发（用于失败收尾）。
func (t *chatToResponsesStream) completedToolItems() []responseOutputItem {
	var output []responseOutputItem
	for _, idx := range t.toolOrder {
		if st := t.tools[idx]; st != nil && st.done && st.completed != nil {
			output = append(output, responseOutputItem{index: st.index, item: st.completed})
		}
	}
	return output
}

// streamsToolArgumentDeltas 判定工具是否在流中逐片下发参数增量。
// 只有 custom 与 tool_search 需要整包收口（协议上不流式输出其输入），
// function 与 namespace 子函数保持增量转发。
func streamsToolArgumentDeltas(kind responseToolKind) bool {
	return kind != responseToolCustom && kind != responseToolSearch
}
