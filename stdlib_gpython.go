package goa

import (
	"encoding/json"
	"fmt"

	"github.com/go-python/gpython/py"
)

// registerGoBackedStdlib installs Go-native implementations of stdlib modules
// gpython does not ship ("batteries not included"). The win is twofold: our
// Python backends keep importing the real module names, and the hot,
// C-accelerated-in-CPython modules run at Go speed instead of interpreted.
//
// This file ships `json` (encoding/json). The same pattern extends to re
// (regexp), hashlib (crypto), base64 (encoding/base64), datetime (time), uuid —
// each a thin module whose methods marshal py.Object <-> Go and call Go stdlib.
func registerGoBackedStdlib() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "json",
			Doc:  "Go-backed json (encoding/json) for gpython.",
		},
		Methods: []*py.Method{
			py.MustNewMethod("dumps", jsonDumps, 0, "dumps(obj) -> str"),
			py.MustNewMethod("loads", jsonLoads, 0, "loads(s) -> obj"),
		},
	})
}

func jsonDumps(_ py.Object, args py.Tuple) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "dumps() missing 1 argument")
	}
	gv, err := pyToGo(args[0])
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(gv)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "json.dumps: %v", err)
	}
	return py.String(b), nil
}

func jsonLoads(_ py.Object, args py.Tuple) (py.Object, error) {
	if len(args) < 1 {
		return nil, py.ExceptionNewf(py.TypeError, "loads() missing 1 argument")
	}
	var s string
	switch v := args[0].(type) {
	case py.String:
		s = string(v)
	case py.Bytes:
		s = string(v)
	default:
		return nil, py.ExceptionNewf(py.TypeError, "loads() expects str/bytes, got %s", args[0].Type().Name)
	}
	var gv interface{}
	if err := json.Unmarshal([]byte(s), &gv); err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "json.loads: %v", err)
	}
	return goToPy(gv), nil
}

// pyToGo converts a gpython object into a json-marshalable Go value.
func pyToGo(o py.Object) (interface{}, error) {
	switch v := o.(type) {
	case nil, py.NoneType:
		return nil, nil
	case py.String:
		return string(v), nil
	case py.Bytes:
		return string(v), nil
	case py.Bool:
		return bool(v), nil
	case py.Int:
		return int64(v), nil
	case py.Float:
		return float64(v), nil
	case py.Tuple:
		return seqToGo(v)
	case *py.List:
		return seqToGo(v.Items)
	case py.StringDict:
		// gpython represents every dict as a string-keyed StringDict.
		m := make(map[string]interface{}, len(v))
		for k, val := range v {
			gv, err := pyToGo(val)
			if err != nil {
				return nil, err
			}
			m[k] = gv
		}
		return m, nil
	default:
		return nil, py.ExceptionNewf(py.TypeError, "json: cannot serialize %s", o.Type().Name)
	}
}

func seqToGo(items []py.Object) (interface{}, error) {
	out := make([]interface{}, len(items))
	for i, it := range items {
		gv, err := pyToGo(it)
		if err != nil {
			return nil, err
		}
		out[i] = gv
	}
	return out, nil
}

// goToPy converts a decoded JSON Go value into gpython objects.
func goToPy(v interface{}) py.Object {
	switch t := v.(type) {
	case nil:
		return py.None
	case bool:
		if t {
			return py.True
		}
		return py.False
	case string:
		return py.String(t)
	case float64:
		// json numbers decode to float64; keep integers integral where exact.
		if t == float64(int64(t)) {
			return py.Int(int64(t))
		}
		return py.Float(t)
	case []interface{}:
		items := make([]py.Object, len(t))
		for i, e := range t {
			items[i] = goToPy(e)
		}
		return py.NewListFromItems(items)
	case map[string]interface{}:
		d := py.NewStringDict()
		for k, e := range t {
			d[k] = goToPy(e)
		}
		return d
	default:
		return py.String(fmt.Sprintf("%v", t))
	}
}
