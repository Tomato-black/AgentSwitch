package claudecfg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
)

const maxDocBytes = 1 << 20

// ErrTooLarge 表示 settings 文件超过页内编辑上限。
var ErrTooLarge = errors.New("settings 文件超过 1 MiB，拒绝页内编辑")

// ParseError 是带行列的 JSON 解析错误。
type ParseError struct {
	Line int
	Col  int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("第 %d 行第 %d 列：%s", e.Line, e.Col, e.Msg)
}

// Doc 是一份 settings 文档及其打开时基线。
type Doc struct {
	Raw      map[string]any
	Original map[string]any
	Hash     string
	MTime    time.Time
}

// ParseDoc 严格解析 JSON 对象（拒绝注释、尾逗号与根非对象）。
func ParseDoc(data []byte) (*Doc, error) {
	if len(data) > maxDocBytes {
		return nil, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, parseErrorAt(data, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, &ParseError{Line: 1, Col: 1, Msg: "JSON 后存在多余内容"}
		}
		return nil, parseErrorAt(data, err)
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, &ParseError{Line: 1, Col: 1, Msg: "根节点必须是 JSON 对象"}
	}
	hash := sha256.Sum256(data)
	return &Doc{
		Raw:      obj,
		Original: cloneMap(obj),
		Hash:     hex.EncodeToString(hash[:]),
	}, nil
}

// Marshal 序列化为 2 空格缩进 + 末尾换行。
func (d *Doc) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(d.Raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func parseErrorAt(data []byte, err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		line, col := lineCol(data, se.Offset)
		return &ParseError{Line: line, Col: col, Msg: se.Error()}
	}
	return &ParseError{Line: 1, Col: 1, Msg: err.Error()}
}

func lineCol(data []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	line, col := 1, 1
	for _, b := range data[:offset] {
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// Diff 返回 original → current 发生变化的叶子路径。
func Diff(original, current map[string]any) []string {
	out := []string{}
	diffMap(original, current, "", &out)
	sort.Strings(out)
	return out
}

func diffMap(a, b map[string]any, prefix string, out *[]string) {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for k := range keys {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		va, oka := a[k]
		vb, okb := b[k]
		ma, isMapA := va.(map[string]any)
		mb, isMapB := vb.(map[string]any)
		if oka && okb && isMapA && isMapB {
			diffMap(ma, mb, path, out)
			continue
		}
		// 一侧缺整棵子树时展开叶子，与 Validate/leafPaths 的路径粒度一致。
		if oka && !okb && isMapA {
			leavesInto(ma, path, out)
			continue
		}
		if !oka && okb && isMapB {
			leavesInto(mb, path, out)
			continue
		}
		if !oka || !okb || !reflect.DeepEqual(va, vb) {
			*out = append(*out, path)
		}
	}
}

// leavesInto 把子树的全部叶子路径追加进 out；空对象按一个叶子处理。
func leavesInto(m map[string]any, prefix string, out *[]string) {
	if len(m) == 0 {
		*out = append(*out, prefix)
		return
	}
	for k, v := range m {
		path := prefix + "." + k
		if child, ok := v.(map[string]any); ok {
			leavesInto(child, path, out)
			continue
		}
		*out = append(*out, path)
	}
}

// ApplyPatch 以 base 为底，只应用 changed 路径在 current 中的值；current 中
// 不存在的路径表示删除。base 的其它键与 json.Number 精度原样保留。
func ApplyPatch(base map[string]any, changed []string, current map[string]any) map[string]any {
	out := cloneMap(base)
	for _, path := range changed {
		if v, ok := Get(current, path); ok {
			Set(out, path, cloneAny(v))
		} else {
			Delete(out, path)
		}
	}
	return out
}

// Get 按点分路径读取。第二个返回值报告路径是否存在。
func Get(root map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// Set 按点分路径写入，缺失的父对象自动创建。
func Set(root map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			cur[p] = value
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// Delete 按点分路径删除。
func Delete(root map[string]any, path string) {
	parts := strings.Split(path, ".")
	cur := root
	for i, p := range parts {
		if i == len(parts)-1 {
			delete(cur, p)
			return
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			return
		}
		cur = next
	}
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMap(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = cloneAny(item)
		}
		return out
	default:
		return v
	}
}
