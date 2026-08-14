//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/jsonata-go/jsonata"
	"gopkg.in/yaml.v3"
)

type mappingFile struct {
	Mappings map[string]mapping `yaml:"mappings"`
}

type mapping struct {
	BAP string `yaml:"bapMappings"`
	BPP string `yaml:"bppMappings"`
}

type compiledMapping struct {
	bap jsonata.Expression
	bpp jsonata.Expression
}

type flow struct {
	name   string
	v1File string
	v2File string
	toV2   direction
	toV1   direction
}

type direction struct {
	action string
	role   string
}

type result struct {
	name          string
	v1ToV2OK      bool
	v2ToV1OK      bool
	v1RoundTripOK bool
	v2RoundTripOK bool
	errs          []string
}

func main() {
	mapperDir := sourceDir()
	payloadDir := filepath.Dir(mapperDir)
	mappingPath := filepath.Join(mapperDir, "mappings.yaml")

	engine, actions, err := loadMappings(mappingPath)
	if err != nil {
		fail("compile mappings", err)
	}

	fmt.Printf("Mapper file: %s\n", mappingPath)
	fmt.Printf("Payload dir: %s\n", payloadDir)
	fmt.Printf("Compiled actions: %s\n\n", strings.Join(actions, ", "))

	flows := []flow{
		{name: "discover <-> discover", v1File: "discover.json", v2File: "v2/discover.json", toV2: direction{"discover", "bap"}, toV1: direction{"discover", "bpp"}},
		{name: "on_discover <-> on_discover", v1File: "on_discover.json", v2File: "v2/on_discover.json", toV2: direction{"on_discover", "bpp"}, toV1: direction{"on_discover", "bap"}},
		{name: "select <-> select", v1File: "select.json", v2File: "v2/select.json", toV2: direction{"select", "bap"}, toV1: direction{"select", "bpp"}},
		{name: "on_select <-> on_select", v1File: "on_select.json", v2File: "v2/on_select.json", toV2: direction{"on_select", "bpp"}, toV1: direction{"on_select", "bap"}},
		{name: "init <-> init", v1File: "init.json", v2File: "v2/init.json", toV2: direction{"init", "bap"}, toV1: direction{"init", "bpp"}},
		{name: "on_init <-> on_init", v1File: "on_init.json", v2File: "v2/on_init.json", toV2: direction{"on_init", "bpp"}, toV1: direction{"on_init", "bap"}},
		{name: "confirm <-> confirm", v1File: "confirm.json", v2File: "v2/confirm.json", toV2: direction{"confirm", "bap"}, toV1: direction{"confirm", "bpp"}},
		{name: "on_confirm <-> on_confirm", v1File: "on_confirm.json", v2File: "v2/on_confirm.json", toV2: direction{"on_confirm", "bpp"}, toV1: direction{"on_confirm", "bap"}},
		{name: "on_update <-> on_update", v1File: "on_update.json", v2File: "v2/on_update.json", toV2: direction{"on_update", "bpp"}, toV1: direction{"on_update", "bap"}},
	}
	flows = filterFlows(flows, os.Args[1:])

	var results []result
	for _, fl := range flows {
		results = append(results, verifyFlow(engine, payloadDir, fl))
	}

	fmt.Println("Summary:")
	allOK := true
	for _, r := range results {
		if !(r.v1ToV2OK && r.v2ToV1OK && r.v1RoundTripOK && r.v2RoundTripOK) {
			allOK = false
		}
		fmt.Printf("- %-28s v1->v2 fixture:%-5v v2->v1 fixture:%-5v v1 roundtrip:%-5v v2 roundtrip:%-5v\n",
			r.name, r.v1ToV2OK, r.v2ToV1OK, r.v1RoundTripOK, r.v2RoundTripOK)
		for _, e := range r.errs {
			fmt.Printf("  %s\n", e)
		}
	}

	if !allOK {
		os.Exit(1)
	}
}

func verifyFlow(engine map[string]compiledMapping, payloadDir string, fl flow) result {
	r := result{name: fl.name}
	v1Path := filepath.Join(payloadDir, fl.v1File)
	v2Path := filepath.Join(payloadDir, fl.v2File)

	v1Bytes, err := os.ReadFile(v1Path)
	if err != nil {
		r.errs = append(r.errs, fmt.Sprintf("read %s: %v", v1Path, err))
		return r
	}
	v2Bytes, err := os.ReadFile(v2Path)
	if err != nil {
		r.errs = append(r.errs, fmt.Sprintf("read %s: %v", v2Path, err))
		return r
	}

	v1ToV2Bytes, err := transform(engine, fl.toV2.action, fl.toV2.role, v1Bytes)
	if err != nil {
		r.errs = append(r.errs, fmt.Sprintf("v1->v2 transform failed: %v", err))
	} else {
		r.v1ToV2OK = sameJSON(v1ToV2Bytes, v2Bytes, &r, "v1->v2 fixture")
	}

	v2ToV1Bytes, err := transform(engine, fl.toV1.action, fl.toV1.role, v2Bytes)
	if err != nil {
		r.errs = append(r.errs, fmt.Sprintf("v2->v1 transform failed: %v", err))
	} else {
		r.v2ToV1OK = sameJSON(v2ToV1Bytes, v1Bytes, &r, "v2->v1 fixture")
	}

	if v1ToV2Bytes != nil {
		backToV1, err := transform(engine, fl.toV1.action, fl.toV1.role, v1ToV2Bytes)
		if err != nil {
			r.errs = append(r.errs, fmt.Sprintf("v1 roundtrip transform failed: %v", err))
		} else {
			r.v1RoundTripOK = sameJSON(backToV1, v1Bytes, &r, "v1 roundtrip")
		}
	}

	if v2ToV1Bytes != nil {
		backToV2, err := transform(engine, fl.toV2.action, fl.toV2.role, v2ToV1Bytes)
		if err != nil {
			r.errs = append(r.errs, fmt.Sprintf("v2 roundtrip transform failed: %v", err))
		} else {
			r.v2RoundTripOK = sameJSON(backToV2, v2Bytes, &r, "v2 roundtrip")
		}
	}

	return r
}

func loadMappings(path string) (map[string]compiledMapping, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var parsed mappingFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, nil, err
	}
	if len(parsed.Mappings) == 0 {
		return nil, nil, fmt.Errorf("no mappings found")
	}

	instance, err := jsonata.OpenLatest()
	if err != nil {
		return nil, nil, err
	}

	out := map[string]compiledMapping{}
	var actions []string
	for action, m := range parsed.Mappings {
		bap, err := instance.Compile(m.BAP, false)
		if err != nil {
			return nil, nil, fmt.Errorf("compile %s bapMappings: %w", action, err)
		}
		bpp, err := instance.Compile(m.BPP, false)
		if err != nil {
			return nil, nil, fmt.Errorf("compile %s bppMappings: %w", action, err)
		}
		out[action] = compiledMapping{bap: bap, bpp: bpp}
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return out, actions, nil
}

func transform(engine map[string]compiledMapping, action, role string, input []byte) ([]byte, error) {
	cm, ok := engine[action]
	if !ok {
		return input, nil
	}

	var expr jsonata.Expression
	switch role {
	case "bap":
		expr = cm.bap
	case "bpp":
		expr = cm.bpp
	default:
		return nil, fmt.Errorf("unknown role %q", role)
	}
	return expr.Evaluate(input, nil)
}

func sameJSON(gotBytes, wantBytes []byte, r *result, label string) bool {
	var got, want any
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		r.errs = append(r.errs, fmt.Sprintf("%s generated invalid JSON: %v", label, err))
		return false
	}
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		r.errs = append(r.errs, fmt.Sprintf("%s expected fixture invalid JSON: %v", label, err))
		return false
	}
	normalizeFilterExpressions(got)
	normalizeFilterExpressions(want)
	if reflect.DeepEqual(got, want) {
		return true
	}

	var diffs []string
	diffJSON("$", got, want, &diffs, 8)
	r.errs = append(r.errs, fmt.Sprintf("%s mismatch: %d sample diff(s): %s", label, len(diffs), strings.Join(diffs, " | ")))
	return false
}

func filterFlows(flows []flow, args []string) []flow {
	if len(args) == 0 {
		return flows
	}
	var out []flow
	for _, fl := range flows {
		for _, arg := range args {
			if strings.Contains(fl.name, arg) || strings.Contains(fl.v1File, arg) || strings.Contains(fl.v2File, arg) {
				out = append(out, fl)
				break
			}
		}
	}
	return out
}

func normalizeFilterExpressions(v any) {
	switch x := v.(type) {
	case map[string]any:
		if expr, ok := x["expression"].(string); ok {
			var parsed any
			if json.Unmarshal([]byte(expr), &parsed) == nil {
				x["expression"] = parsed
			}
		}
		for _, child := range x {
			normalizeFilterExpressions(child)
		}
	case []any:
		for _, child := range x {
			normalizeFilterExpressions(child)
		}
	}
}

func diffJSON(path string, got, want any, diffs *[]string, limit int) {
	if len(*diffs) >= limit {
		return
	}
	if reflect.DeepEqual(got, want) {
		return
	}

	gm, gok := got.(map[string]any)
	wm, wok := want.(map[string]any)
	if gok && wok {
		keys := map[string]bool{}
		for k := range gm {
			keys[k] = true
		}
		for k := range wm {
			keys[k] = true
		}
		var sorted []string
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			_, hasG := gm[k]
			_, hasW := wm[k]
			child := path + "." + k
			if !hasG || !hasW {
				*diffs = append(*diffs, fmt.Sprintf("%s got=%s want=%s", child, compact(gm[k], hasG), compact(wm[k], hasW)))
			} else {
				diffJSON(child, gm[k], wm[k], diffs, limit)
			}
			if len(*diffs) >= limit {
				return
			}
		}
		return
	}

	ga, gok := got.([]any)
	wa, wok := want.([]any)
	if gok && wok {
		if len(ga) != len(wa) {
			*diffs = append(*diffs, fmt.Sprintf("%s length got=%d want=%d", path, len(ga), len(wa)))
			return
		}
		for i := range ga {
			diffJSON(fmt.Sprintf("%s[%d]", path, i), ga[i], wa[i], diffs, limit)
			if len(*diffs) >= limit {
				return
			}
		}
		return
	}

	*diffs = append(*diffs, fmt.Sprintf("%s got=%s want=%s", path, compact(got, true), compact(want, true)))
}

func compact(v any, present bool) string {
	if !present {
		return "<missing>"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if len(s) > 180 {
		return s[:177] + "..."
	}
	return s
}

func sourceDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fail("locate source", fmt.Errorf("runtime.Caller failed"))
	}
	return filepath.Dir(file)
}

func fail(label string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
	os.Exit(1)
}
