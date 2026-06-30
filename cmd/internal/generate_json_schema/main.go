package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

type schema map[string]any

type optionStruct struct {
	Name   string
	Fields []optionField
}

type optionField struct {
	Name      string
	JSONName  string
	OmitEmpty bool
	Anonymous bool
	Type      ast.Expr
	Doc       string
}

type registryCall struct {
	Category   string
	JSONType   string
	OptionType string
}

type Generator struct {
	root       string
	structs    map[string]*optionStruct
	aliases    map[string]ast.Expr
	constants  map[string]any
	registries map[string]map[string]string
	defs       map[string]schema
	inProgress map[string]bool
}

func main() {
	var output string
	var check bool
	flag.StringVar(&output, "output", "docs/configuration/schema.json", "schema output path")
	flag.BoolVar(&check, "check", false, "verify output file is up to date")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	generator := newGenerator(root)
	err = generator.load()
	if err != nil {
		log.Fatal(err)
	}
	content, err := generator.generate()
	if err != nil {
		log.Fatal(err)
	}
	if check {
		current, err := os.ReadFile(output)
		if err != nil {
			log.Fatal(err)
		}
		if !bytes.Equal(current, content) {
			log.Fatal(output, " is out of date; run `make generate_json_schema`")
		}
		return
	}
	err = os.MkdirAll(filepath.Dir(output), os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}
	err = os.WriteFile(output, content, 0o644)
	if err != nil {
		log.Fatal(err)
	}
}

func newGenerator(root string) *Generator {
	return &Generator{
		root:       root,
		structs:    make(map[string]*optionStruct),
		aliases:    make(map[string]ast.Expr),
		constants:  make(map[string]any),
		registries: make(map[string]map[string]string),
		defs:       make(map[string]schema),
		inProgress: make(map[string]bool),
	}
}

func (g *Generator) load() error {
	if err := g.loadConstants(); err != nil {
		return err
	}
	if err := g.loadOptionPackage(); err != nil {
		return err
	}
	return g.scanRegistries()
}

func (g *Generator) loadConstants() error {
	for _, relDir := range []string{"constant", "option"} {
		dir := filepath.Join(g.root, relDir)
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range files {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
			if err != nil {
				return err
			}
			g.collectConstants(relDir, file)
		}
	}
	return nil
}

func (g *Generator) collectConstants(packageName string, file *ast.File) {
	var iotaIndex int
	var lastValues []ast.Expr
	for _, decl := range file.Decls {
		genDecl, isGenDecl := decl.(*ast.GenDecl)
		if !isGenDecl || genDecl.Tok != token.CONST {
			continue
		}
		iotaIndex = 0
		lastValues = nil
		for _, spec := range genDecl.Specs {
			valueSpec := spec.(*ast.ValueSpec)
			values := valueSpec.Values
			if len(values) == 0 {
				values = lastValues
			} else {
				lastValues = values
			}
			for i, name := range valueSpec.Names {
				if i >= len(values) {
					continue
				}
				value, loaded := g.evalConst(values[i], iotaIndex)
				if !loaded {
					continue
				}
				g.constants[packageName+"."+name.Name] = value
			}
			iotaIndex++
		}
	}
}

func (g *Generator) evalConst(expr ast.Expr, iotaIndex int) (any, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		switch value.Kind {
		case token.STRING:
			unquoted, err := strconv.Unquote(value.Value)
			return unquoted, err == nil
		case token.INT:
			parsed, err := strconv.ParseInt(value.Value, 0, 64)
			return parsed, err == nil
		}
	case *ast.Ident:
		if value.Name == "iota" {
			return int64(iotaIndex), true
		}
		resolved, loaded := g.constants["constant."+value.Name]
		if !loaded {
			resolved, loaded = g.constants["option."+value.Name]
		}
		return resolved, loaded
	case *ast.CallExpr:
		if len(value.Args) == 1 {
			return g.evalConst(value.Args[0], iotaIndex)
		}
	case *ast.BinaryExpr:
		left, leftLoaded := g.evalConst(value.X, iotaIndex)
		right, rightLoaded := g.evalConst(value.Y, iotaIndex)
		if !leftLoaded || !rightLoaded {
			return nil, false
		}
		leftInt, leftIsInt := left.(int64)
		rightInt, rightIsInt := right.(int64)
		if !leftIsInt || !rightIsInt {
			return nil, false
		}
		switch value.Op {
		case token.ADD:
			return leftInt + rightInt, true
		}
	}
	return nil, false
}

func (g *Generator) loadOptionPackage() error {
	dir := filepath.Join(g.root, "option")
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			genDecl, isGenDecl := decl.(*ast.GenDecl)
			if !isGenDecl || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				if structType, isStructType := typeSpec.Type.(*ast.StructType); isStructType {
					g.structs[typeSpec.Name.Name] = parseStruct(typeSpec.Name.Name, structType)
				} else {
					g.aliases[typeSpec.Name.Name] = typeSpec.Type
				}
			}
		}
	}
	return nil
}

func parseStruct(name string, structType *ast.StructType) *optionStruct {
	var fields []optionField
	for _, field := range structType.Fields.List {
		jsonName, omitEmpty, hasJSON := parseJSONTag(field.Tag)
		doc := ""
		if field.Doc != nil {
			doc = field.Doc.Text()
		}
		if len(field.Names) == 0 {
			fields = append(fields, optionField{
				JSONName:  jsonName,
				OmitEmpty: omitEmpty,
				Anonymous: true,
				Type:      field.Type,
				Doc:       doc,
			})
			continue
		}
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			fieldJSONName := jsonName
			if !hasJSON {
				fieldJSONName = name.Name
			}
			fields = append(fields, optionField{
				Name:      name.Name,
				JSONName:  fieldJSONName,
				OmitEmpty: omitEmpty,
				Type:      field.Type,
				Doc:       doc,
			})
		}
	}
	return &optionStruct{Name: name, Fields: fields}
}

func parseJSONTag(tag *ast.BasicLit) (name string, omitEmpty bool, found bool) {
	if tag == nil {
		return "", false, false
	}
	raw, err := strconv.Unquote(tag.Value)
	if err != nil {
		return "", false, false
	}
	for _, part := range strings.Split(raw, " ") {
		if !strings.HasPrefix(part, "json:") {
			continue
		}
		value, err := strconv.Unquote(strings.TrimPrefix(part, "json:"))
		if err != nil {
			return "", false, false
		}
		items := strings.Split(value, ",")
		name = items[0]
		for _, item := range items[1:] {
			if item == "omitempty" {
				omitEmpty = true
			}
		}
		return name, omitEmpty, true
	}
	return "", false, false
}

func (g *Generator) scanRegistries() error {
	return filepath.WalkDir(g.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		imports := importAliases(file)
		ast.Inspect(file, func(node ast.Node) bool {
			callExpr, isCallExpr := node.(*ast.CallExpr)
			if !isCallExpr {
				return true
			}
			registryCall, isCallExpr := g.parseRegistryCall(callExpr, imports)
			if !isCallExpr {
				return true
			}
			if g.registries[registryCall.Category] == nil {
				g.registries[registryCall.Category] = make(map[string]string)
			}
			g.registries[registryCall.Category][registryCall.JSONType] = registryCall.OptionType
			return true
		})
		return nil
	})
}

func importAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, importSpec := range file.Imports {
		pathValue, err := strconv.Unquote(importSpec.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		if importSpec.Name != nil {
			name = importSpec.Name.Name
		} else {
			name = path.Base(pathValue)
		}
		aliases[name] = pathValue
	}
	return aliases
}

func (g *Generator) parseRegistryCall(call *ast.CallExpr, imports map[string]string) (registryCall, bool) {
	var selector *ast.SelectorExpr
	var typeArgs []ast.Expr
	switch fun := call.Fun.(type) {
	case *ast.IndexExpr:
		selector, _ = fun.X.(*ast.SelectorExpr)
		typeArgs = []ast.Expr{fun.Index}
	case *ast.IndexListExpr:
		selector, _ = fun.X.(*ast.SelectorExpr)
		typeArgs = fun.Indices
	default:
		return registryCall{}, false
	}
	if selector == nil || len(typeArgs) == 0 || len(call.Args) < 2 {
		return registryCall{}, false
	}
	receiver, isIdent := selector.X.(*ast.Ident)
	if !isIdent {
		return registryCall{}, false
	}
	importPath := imports[receiver.Name]
	category := registryCategory(importPath, selector.Sel.Name)
	if category == "" {
		return registryCall{}, false
	}
	optionType := optionTypeName(typeArgs[0])
	if optionType == "" {
		return registryCall{}, false
	}
	jsonType, isIdent := g.resolveStringExpr(call.Args[1], imports)
	if !isIdent || jsonType == "" {
		return registryCall{}, false
	}
	return registryCall{
		Category:   category,
		JSONType:   jsonType,
		OptionType: optionType,
	}, true
}

func registryCategory(importPath string, functionName string) string {
	switch importPath {
	case "github.com/sagernet/sing-box/adapter/inbound":
		if functionName == "Register" {
			return "Inbound"
		}
	case "github.com/sagernet/sing-box/adapter/outbound":
		if functionName == "Register" {
			return "Outbound"
		}
	case "github.com/sagernet/sing-box/adapter/endpoint":
		if functionName == "Register" {
			return "Endpoint"
		}
	case "github.com/sagernet/sing-box/adapter/provider":
		if functionName == "Register" {
			return "Provider"
		}
	case "github.com/sagernet/sing-box/adapter/service":
		if functionName == "Register" {
			return "Service"
		}
	case "github.com/sagernet/sing-box/adapter/certificate":
		if functionName == "Register" {
			return "CertificateProvider"
		}
	case "github.com/sagernet/sing-box/dns":
		if functionName == "RegisterTransport" {
			return "DNSTransport"
		}
	}
	return ""
}

func optionTypeName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	}
	return ""
}

func (g *Generator) resolveStringExpr(expr ast.Expr, imports map[string]string) (string, bool) {
	value, loaded := g.resolveConstExpr(expr, imports)
	if !loaded {
		return "", false
	}
	stringValue, isString := value.(string)
	return stringValue, isString
}

func (g *Generator) resolveConstExpr(expr ast.Expr, imports map[string]string) (any, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		switch value.Kind {
		case token.STRING:
			unquoted, err := strconv.Unquote(value.Value)
			return unquoted, err == nil
		case token.INT:
			parsed, err := strconv.ParseInt(value.Value, 0, 64)
			return parsed, err == nil
		}
	case *ast.Ident:
		resolved, loaded := g.constants["option."+value.Name]
		return resolved, loaded
	case *ast.SelectorExpr:
		receiver, isIdent := value.X.(*ast.Ident)
		if !isIdent {
			return nil, false
		}
		importPath := imports[receiver.Name]
		switch importPath {
		case "github.com/sagernet/sing-box/constant":
			resolved, loaded := g.constants["constant."+value.Sel.Name]
			return resolved, loaded
		case "github.com/sagernet/sing-box/option":
			resolved, loaded := g.constants["option."+value.Sel.Name]
			return resolved, loaded
		}
	}
	return nil, false
}

func (g *Generator) generate() ([]byte, error) {
	root := schema{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://sing-box.sagernet.org/schema.json",
		"title":   "sing-box configuration",
		"$ref":    "#/$defs/Options",
	}
	_, err := g.definition("Options")
	if err != nil {
		return nil, err
	}
	root["$defs"] = g.defs
	content, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func (g *Generator) definition(name string) (schema, error) {
	if existing, loaded := g.defs[name]; loaded {
		return existing, nil
	}
	if g.inProgress[name] {
		return schema{"$ref": "#/$defs/" + name}, nil
	}
	g.inProgress[name] = true
	defer delete(g.inProgress, name)

	def, err := g.buildDefinition(name)
	if err != nil {
		return nil, err
	}
	g.defs[name] = def
	return def, nil
}

func (g *Generator) buildDefinition(name string) (schema, error) {
	switch name {
	case "Inbound":
		return g.registrySchema("_Inbound", "type", g.registries["Inbound"], nil, false)
	case "Outbound":
		return g.registrySchema("_Outbound", "type", g.registries["Outbound"], nil, false)
	case "Endpoint":
		return g.registrySchema("_Endpoint", "type", g.registries["Endpoint"], nil, false)
	case "Provider":
		return g.registrySchema("_Provider", "type", g.registries["Provider"], nil, false)
	case "Service":
		return g.registrySchema("_Service", "type", g.registries["Service"], nil, false)
	case "CertificateProvider":
		return g.registrySchema("_CertificateProvider", "type", g.registries["CertificateProvider"], nil, false)
	case "DNSServerOptions":
		return g.registrySchema("_DNSServerOptions", "type", g.registries["DNSTransport"], nil, false)
	case "CertificateProviderOptions":
		object, err := g.registrySchema("_CertificateProviderInline", "type", g.registries["CertificateProvider"], nil, false)
		if err != nil {
			return nil, err
		}
		return schema{"anyOf": []any{schema{"type": "string"}, object}}, nil
	case "HTTPClient":
		return g.httpClientSchema(false)
	case "HTTPClientOptions":
		return g.httpClientSchema(true)
	case "Rule":
		return g.variantSchema("_Rule", "type", map[string]string{
			"default": "DefaultRule",
			"logical": "LogicalRule",
		}, "default", false)
	case "DefaultRule":
		return g.combineRef("RawDefaultRule", "RuleAction", nil)
	case "LogicalRule":
		return g.combineRef("RawLogicalRule", "RuleAction", nil)
	case "DNSRule":
		return g.variantSchema("_DNSRule", "type", map[string]string{
			"default": "DefaultDNSRule",
			"logical": "LogicalDNSRule",
		}, "default", false)
	case "DefaultDNSRule":
		return g.combineRef("RawDefaultDNSRule", "DNSRuleAction", nil)
	case "LogicalDNSRule":
		return g.combineRef("RawLogicalDNSRule", "DNSRuleAction", nil)
	case "HeadlessRule":
		return g.variantSchema("_HeadlessRule", "type", map[string]string{
			"default": "DefaultHeadlessRule",
			"logical": "LogicalHeadlessRule",
		}, "default", false)
	case "RuleAction":
		return g.variantSchema("_RuleAction", "action", map[string]string{
			"route":         "RouteActionOptions",
			"route-options": "RouteOptionsActionOptions",
			"direct":        "DirectActionOptions",
			"bypass":        "RouteActionOptions",
			"reject":        "RejectActionOptions",
			"hijack-dns":    "",
			"sniff":         "RouteActionSniff",
			"resolve":       "RouteActionResolve",
		}, "route", false)
	case "DNSRuleAction":
		return g.variantSchema("_DNSRuleAction", "action", map[string]string{
			"route":         "DNSRouteActionOptions",
			"evaluate":      "DNSRouteActionOptions",
			"respond":       "",
			"route-options": "DNSRouteOptionsActionOptions",
			"reject":        "RejectActionOptions",
			"predefined":    "DNSRouteActionPredefined",
		}, "route", false)
	case "RuleSet":
		return g.variantSchema("_RuleSet", "type", map[string]string{
			"inline": "PlainRuleSet",
			"local":  "LocalRuleSet",
			"remote": "RemoteRuleSet",
		}, "inline", false)
	case "PlainRuleSetCompat":
		return g.combineRef("_PlainRuleSetCompat", "PlainRuleSet", nil)
	case "V2RayTransportOptions":
		return g.variantSchema("_V2RayTransportOptions", "type", map[string]string{
			"http":        "V2RayHTTPOptions",
			"ws":          "V2RayWebsocketOptions",
			"quic":        "V2RayQUICOptions",
			"grpc":        "V2RayGRPCOptions",
			"httpupgrade": "V2RayHTTPUpgradeOptions",
		}, "", false)
	case "ACMEDNS01ChallengeOptions":
		return g.variantSchema("_ACMEDNS01ChallengeOptions", "provider", dnsProviderVariants(), "", false)
	case "ACMEProviderDNS01ChallengeOptions":
		return g.variantSchema("_ACMEProviderDNS01ChallengeOptions", "provider", dnsProviderVariants(), "", false)
	case "Hysteria2Obfs":
		return g.variantSchema("_Hysteria2Obfs", "type", map[string]string{
			"salamander": "",
			"gecko":      "Hysteria2ObfsGecko",
		}, "", false)
	case "Hysteria2Masquerade":
		object, err := g.variantSchema("_Hysteria2Masquerade", "type", map[string]string{
			"file":   "Hysteria2MasqueradeFile",
			"proxy":  "Hysteria2MasqueradeProxy",
			"string": "Hysteria2MasqueradeString",
		}, "", false)
		if err != nil {
			return nil, err
		}
		return schema{"anyOf": []any{schema{"type": "string"}, object}}, nil
	case "DomainResolveOptions":
		objectRef, err := g.ref("_DomainResolveOptions")
		if err != nil {
			return nil, err
		}
		return schema{"anyOf": []any{schema{"type": "string"}, objectRef}}, nil
	case "UDPOverTCPOptions":
		objectRef, err := g.ref("_UDPOverTCPOptions")
		if err != nil {
			return nil, err
		}
		return schema{"anyOf": []any{schema{"type": "boolean"}, objectRef}}, nil
	case "OptimisticDNSOptions":
		objectRef, err := g.ref("_OptimisticDNSOptions")
		if err != nil {
			return nil, err
		}
		return schema{"anyOf": []any{schema{"type": "boolean"}, objectRef}}, nil
	}
	if enum := g.namedEnum(name); enum != nil {
		return enum, nil
	}
	if structType, loaded := g.structs[name]; loaded {
		return g.structSchema(structType)
	}
	if alias, loaded := g.aliases[name]; loaded {
		return g.schemaFor(alias)
	}
	return nil, E.New("unknown option type: ", name)
}

func dnsProviderVariants() map[string]string {
	return map[string]string{
		"alidns":     "ACMEDNS01AliDNSOptions",
		"cloudflare": "ACMEDNS01CloudflareOptions",
		"acmedns":    "ACMEDNS01ACMEDNSOptions",
	}
}

func (g *Generator) registrySchema(base string, discriminator string, variants map[string]string, defaultVariant *string, allowString bool) (schema, error) {
	if len(variants) == 0 {
		return nil, E.New("no variants found for ", base)
	}
	result, err := g.variantSchema(base, discriminator, variants, common.PtrValueOrDefault(defaultVariant), false)
	if err != nil {
		return nil, err
	}
	if allowString {
		return schema{"anyOf": []any{schema{"type": "string"}, result}}, nil
	}
	return result, nil
}

func (g *Generator) variantSchema(base string, discriminator string, variants map[string]string, defaultVariant string, allowString bool) (schema, error) {
	keys := sortedKeys(variants)
	oneOf := make([]any, 0, len(keys))
	for _, value := range keys {
		optionType := variants[value]
		var extra []schema
		if optionType != "" {
			ref, err := g.ref(optionType)
			if err != nil {
				return nil, err
			}
			extra = append(extra, ref)
		}
		override := schema{
			"type": "object",
			"properties": schema{
				discriminator: schema{"const": value},
			},
		}
		if value == defaultVariant {
			override["properties"].(schema)[discriminator] = schema{"enum": []any{"", value}}
		} else {
			override["required"] = []any{discriminator}
		}
		extra = append(extra, override)
		combined, err := g.combineRef(base, "", extra)
		if err != nil {
			return nil, err
		}
		oneOf = append(oneOf, combined)
	}
	result := schema{"oneOf": oneOf}
	if allowString {
		return schema{"anyOf": []any{schema{"type": "string"}, result}}, nil
	}
	return result, nil
}

func (g *Generator) httpClientSchema(allowTagRef bool) (schema, error) {
	version1, err := g.combineRef("_HTTPClientOptions", "", []schema{
		{
			"type": "object",
			"properties": schema{
				"version": schema{"const": 1},
			},
			"required": []any{"version"},
		},
	})
	if err != nil {
		return nil, err
	}
	version2, err := g.combineRef("_HTTPClientOptions", "HTTP2Options", []schema{
		{
			"type": "object",
			"properties": schema{
				"version": schema{"enum": []any{0, 2}},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	version3, err := g.combineRef("_HTTPClientOptions", "QUICOptions", []schema{
		{
			"type": "object",
			"properties": schema{
				"version": schema{"const": 3},
			},
			"required": []any{"version"},
		},
	})
	if err != nil {
		return nil, err
	}
	object := schema{"oneOf": []any{version1, version2, version3}}
	if allowTagRef {
		return schema{"anyOf": []any{schema{"type": "string"}, object}}, nil
	}
	return object, nil
}

func (g *Generator) combineRef(base string, extraType string, extra []schema) (schema, error) {
	var parts []schema
	baseDef, err := g.definition(base)
	if err != nil {
		return nil, err
	}
	parts = append(parts, baseDef)
	if extraType != "" {
		extraDef, err := g.definition(extraType)
		if err != nil {
			return nil, err
		}
		parts = append(parts, extraDef)
	}
	parts = append(parts, extra...)
	return g.combineObjectSchemas(parts)
}

func (g *Generator) combineObjectSchemas(parts []schema) (schema, error) {
	for index, part := range parts {
		oneOf, loaded := part["oneOf"].([]any)
		if !loaded {
			continue
		}
		var variants []any
		for _, item := range oneOf {
			itemSchema, isSchema := item.(schema)
			if !isSchema {
				return nil, E.New("unsupported non-object oneOf item")
			}
			nextParts := make([]schema, 0, len(parts))
			nextParts = append(nextParts, parts[:index]...)
			nextParts = append(nextParts, itemSchema)
			nextParts = append(nextParts, parts[index+1:]...)
			combined, err := g.combineObjectSchemas(nextParts)
			if err != nil {
				return nil, err
			}
			variants = append(variants, combined)
		}
		return schema{"oneOf": variants}, nil
	}
	properties := schema{}
	requiredSet := map[string]bool{}
	for _, part := range parts {
		g.mergeObjectSchema(properties, requiredSet, part)
	}
	result := schema{
		"type":                 "object",
		"additionalProperties": false,
	}
	if len(properties) > 0 {
		result["properties"] = properties
	}
	required := sortedSet(requiredSet)
	if len(required) > 0 {
		result["required"] = common.Map(required, func(it string) any {
			return it
		})
	}
	return result, nil
}

func (g *Generator) ref(name string) (schema, error) {
	if _, err := g.definition(name); err != nil {
		return nil, err
	}
	return schema{"$ref": "#/$defs/" + name}, nil
}

func (g *Generator) structSchema(structType *optionStruct) (schema, error) {
	properties := schema{}
	requiredSet := map[string]bool{}
	for _, field := range structType.Fields {
		if field.JSONName == "-" {
			continue
		}
		if field.Anonymous && field.JSONName == "" {
			embedded, err := g.schemaFor(field.Type)
			if err != nil {
				return nil, E.Cause(err, structType.Name, " embedded")
			}
			g.mergeObjectSchema(properties, requiredSet, embedded)
			continue
		}
		if field.JSONName == "" {
			continue
		}
		fieldSchema, err := g.schemaFor(field.Type)
		if err != nil {
			return nil, E.Cause(err, structType.Name, ".", field.Name)
		}
		if strings.Contains(field.Doc, "Deprecated:") {
			fieldSchema = maps.Clone(fieldSchema)
			fieldSchema["deprecated"] = true
		}
		properties[field.JSONName] = fieldSchema
		if !field.OmitEmpty {
			requiredSet[field.JSONName] = true
		}
	}
	result := schema{
		"type":                 "object",
		"additionalProperties": false,
	}
	if len(properties) > 0 {
		result["properties"] = properties
	}
	required := sortedSet(requiredSet)
	if len(required) > 0 {
		result["required"] = common.Map(required, func(it string) any {
			return it
		})
	}
	return result, nil
}

func (g *Generator) mergeObjectSchema(properties schema, requiredSet map[string]bool, embedded schema) {
	if ref, loaded := embedded["$ref"].(string); loaded {
		name := strings.TrimPrefix(ref, "#/$defs/")
		definition := g.defs[name]
		g.mergeObjectSchema(properties, requiredSet, definition)
		return
	}
	if allOf, loaded := embedded["allOf"].([]any); loaded {
		for _, item := range allOf {
			if itemSchema, isSchema := item.(schema); isSchema {
				g.mergeObjectSchema(properties, requiredSet, itemSchema)
			}
		}
		return
	}
	if embeddedProperties, loaded := embedded["properties"].(schema); loaded {
		for key, value := range embeddedProperties {
			properties[key] = value
		}
	}
	for _, item := range anyToStrings(embedded["required"]) {
		requiredSet[item] = true
	}
}

func (g *Generator) schemaFor(expr ast.Expr) (schema, error) {
	switch value := expr.(type) {
	case *ast.Ident:
		return g.schemaForIdent(value.Name)
	case *ast.StarExpr:
		item, err := g.schemaFor(value.X)
		if err != nil {
			return nil, err
		}
		return nullable(item), nil
	case *ast.ArrayType:
		if ident, isIdent := value.Elt.(*ast.Ident); isIdent && (ident.Name == "byte" || ident.Name == "uint8") {
			return schema{"type": "string"}, nil
		}
		item, err := g.schemaFor(value.Elt)
		if err != nil {
			return nil, err
		}
		return schema{
			"type":  "array",
			"items": item,
		}, nil
	case *ast.MapType:
		item, err := g.schemaFor(value.Value)
		if err != nil {
			return nil, err
		}
		result := schema{
			"type":                 "object",
			"additionalProperties": item,
		}
		keySchema, err := g.schemaFor(value.Key)
		if err == nil {
			if propertyNames := propertyNamesFromSchema(keySchema); propertyNames != nil {
				result["propertyNames"] = propertyNames
			}
		}
		return result, nil
	case *ast.SelectorExpr:
		return g.schemaForSelector(value)
	case *ast.IndexExpr:
		return g.schemaForGeneric(value.X, []ast.Expr{value.Index})
	case *ast.IndexListExpr:
		return g.schemaForGeneric(value.X, value.Indices)
	case *ast.StructType:
		return g.structSchema(parseStruct("", value))
	case *ast.InterfaceType:
		return schema{}, nil
	}
	return nil, E.New("unsupported type expression ", exprString(expr))
}

func (g *Generator) schemaForIdent(name string) (schema, error) {
	switch name {
	case "string":
		return schema{"type": "string"}, nil
	case "bool":
		return schema{"type": "boolean"}, nil
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return schema{"type": "integer"}, nil
	case "float32", "float64":
		return schema{"type": "number"}, nil
	case "any":
		return schema{}, nil
	}
	if enum := g.namedEnum(name); enum != nil {
		return enum, nil
	}
	if _, loaded := g.structs[name]; loaded {
		return g.ref(name)
	}
	if alias, loaded := g.aliases[name]; loaded {
		if ident, isIdent := alias.(*ast.Ident); isIdent && isBuiltinType(ident.Name) {
			return g.schemaFor(alias)
		}
		return g.ref(name)
	}
	return nil, E.New("unknown identifier type ", name)
}

func (g *Generator) schemaForSelector(selector *ast.SelectorExpr) (schema, error) {
	receiver, isIdent := selector.X.(*ast.Ident)
	if !isIdent {
		return nil, E.New("unsupported selector ", exprString(selector))
	}
	switch receiver.Name + "." + selector.Sel.Name {
	case "badoption.Duration":
		return schema{"type": "string"}, nil
	case "badoption.HTTPHeader":
		listableString := listableSchema(schema{"type": "string"})
		return schema{
			"type":                 "object",
			"additionalProperties": listableString,
		}, nil
	case "badoption.Regexp":
		return schema{"type": "string"}, nil
	case "badoption.Addr", "netip.Addr":
		return schema{"type": "string", "format": "ipvanyaddress"}, nil
	case "netip.AddrPort":
		return schema{"type": "string"}, nil
	case "badoption.Prefix", "badoption.Prefixable", "netip.Prefix":
		return schema{"type": "string", "format": "ipvanynetwork"}, nil
	case "json.RawMessage":
		return schema{}, nil
	case "byteformats.Bytes", "byteformats.MemoryBytes", "byteformats.NetworkBytes", "byteformats.NetworkBytesCompat":
		return schema{"anyOf": []any{schema{"type": "integer"}, schema{"type": "string"}}}, nil
	case "auth.User":
		return schema{
			"type": "object",
			"properties": schema{
				"username": schema{"type": "string"},
				"password": schema{"type": "string"},
			},
			"additionalProperties": false,
		}, nil
	case "M.Socksaddr":
		return schema{"type": "string"}, nil
	}
	return nil, E.New("unknown selector type ", exprString(selector))
}

func (g *Generator) schemaForGeneric(base ast.Expr, args []ast.Expr) (schema, error) {
	selector, isSelectorExpr := base.(*ast.SelectorExpr)
	if !isSelectorExpr {
		return nil, E.New("unsupported generic type ", exprString(base))
	}
	receiver, isIdent := selector.X.(*ast.Ident)
	if !isIdent {
		return nil, E.New("unsupported generic receiver ", exprString(base))
	}
	switch receiver.Name + "." + selector.Sel.Name {
	case "badoption.Listable":
		if len(args) != 1 {
			return nil, E.New("badoption.Listable expects one type parameter")
		}
		item, err := g.schemaFor(args[0])
		if err != nil {
			return nil, err
		}
		return listableSchema(item), nil
	case "badjson.TypedMap":
		if len(args) != 2 {
			return nil, E.New("badjson.TypedMap expects two type parameters")
		}
		key, err := g.schemaFor(args[0])
		if err != nil {
			return nil, err
		}
		value, err := g.schemaFor(args[1])
		if err != nil {
			return nil, err
		}
		result := schema{
			"type":                 "object",
			"additionalProperties": value,
		}
		if propertyNames := propertyNamesFromSchema(key); propertyNames != nil {
			result["propertyNames"] = propertyNames
		}
		return result, nil
	case "badjson.TypedArray":
		if len(args) != 1 {
			return nil, E.New("badjson.TypedArray expects one type parameter")
		}
		item, err := g.schemaFor(args[0])
		if err != nil {
			return nil, err
		}
		return schema{"type": "array", "items": item}, nil
	}
	return nil, E.New("unknown generic type ", exprString(base))
}

func (g *Generator) namedEnum(name string) schema {
	switch name {
	case "DomainStrategy":
		return schema{"type": "string", "enum": []any{"", "as_is", "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only"}}
	case "NetworkStrategy":
		return schema{"type": "string", "enum": []any{"default", "fallback", "hybrid"}}
	case "InterfaceType":
		return schema{"type": "string", "enum": []any{"wifi", "cellular", "ethernet", "other"}}
	case "NetworkList":
		return listableSchema(schema{"type": "string", "enum": []any{"tcp", "udp"}})
	case "DNSQueryType", "DNSRCode":
		return schema{"anyOf": []any{schema{"type": "integer"}, schema{"type": "string"}}}
	case "DNSRecordOptions":
		return schema{"type": "string"}
	case "TimeRange":
		return schema{"type": "string"}
	case "FwMark":
		return schema{"anyOf": []any{schema{"type": "integer"}, schema{"type": "string"}}}
	case "UDPTimeoutCompat":
		return schema{"anyOf": []any{schema{"type": "integer"}, schema{"type": "string"}}}
	case "ClientAuthType":
		return schema{"type": "string", "enum": []any{"no", "request", "require-any", "verify-if-given", "require-and-verify"}}
	case "CurvePreference":
		return schema{"type": "string", "enum": []any{"P256", "P384", "P521", "X25519", "X25519MLKEM768"}}
	case "ACMEKeyType":
		return schema{"type": "string", "enum": []any{"", "ed25519", "p256", "p384", "rsa2048", "rsa4096"}}
	case "CloudflareOriginCARequestType":
		return schema{"type": "string", "enum": []any{"", "origin-rsa", "origin-ecc"}}
	case "CloudflareOriginCARequestValidity":
		return schema{"type": "integer", "enum": []any{0, 7, 30, 90, 365, 730, 1095, 5475}}
	case "WildcardSNI":
		return schema{"type": "string", "enum": []any{"", "off", "authed", "all"}}
	case "OnDemandRuleAction":
		return schema{"type": "string", "enum": []any{"connect", "disconnect", "evaluate_connection", "ignore"}}
	case "OnDemandRuleInterfaceType":
		return schema{"type": "string", "enum": []any{"any", "wifi", "cellular"}}
	case "CertificateOptions":
		return nil
	}
	return nil
}

func listableSchema(item schema) schema {
	return schema{
		"anyOf": []any{
			item,
			schema{
				"type":  "array",
				"items": item,
			},
		},
	}
}

func nullable(item schema) schema {
	return schema{"anyOf": []any{item, schema{"type": "null"}}}
}

func propertyNamesFromSchema(item schema) schema {
	if enum, loaded := item["enum"]; loaded {
		return schema{"enum": enum}
	}
	if typ, loaded := item["type"]; loaded && typ == "string" {
		result := schema{"type": "string"}
		if formatValue, loaded := item["format"]; loaded {
			result["format"] = formatValue
		}
		return result
	}
	return nil
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func sortedSet(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func anyToStrings(value any) []string {
	switch items := value.(type) {
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if stringValue, isString := item.(string); isString {
				result = append(result, stringValue)
			}
		}
		return result
	case []string:
		return items
	}
	return nil
}

func isBuiltinType(name string) bool {
	switch name {
	case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "any":
		return true
	default:
		return false
	}
}

func exprString(expr ast.Expr) string {
	var buffer bytes.Buffer
	err := format.Node(&buffer, token.NewFileSet(), expr)
	if err != nil {
		return fmt.Sprintf("%T", expr)
	}
	return buffer.String()
}
