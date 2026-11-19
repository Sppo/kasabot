package kasabot_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// TestArchitectureBoundaries перевіряє реальний код репозиторію на відповідність ARCHITECTURE_RULES.md.
func TestArchitectureBoundaries(t *testing.T) {
	t.Run("GoBackendBoundaries", func(t *testing.T) {
		for _, v := range checkGoArchitecture(".") {
			t.Error(v)
		}
	})
	t.Run("ReactFrontendBoundaries", func(t *testing.T) {
		for _, v := range checkFrontendArchitecture(".") {
			t.Error(v)
		}
	})
}

// knownModuleTablePrefixes фіксує затверджені в SPEC.md префікси таблиць для кожного модуля.
var knownModuleTablePrefixes = map[string]string{
	"finance": "fin_",
}

func checkGoArchitecture(rootDir string) []string {
	var violations []string
	fset := token.NewFileSet()
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return []string{"resolve rootDir: " + err.Error()}
	}

	walkErr := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return relErr
		}
		relPath := filepath.ToSlash(rel)
		if relPath == "." {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			// Пропускаємо web, bin, dist тільки в корені репозиторію
			if relPath == "web" || relPath == "bin" || relPath == "dist" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(relPath, ".go") || strings.HasSuffix(relPath, "_test.go") {
			return nil
		}

		fullAST, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			violations = append(violations, "[PARSE ERROR] "+relPath+": "+parseErr.Error())
			return nil
		}

		fileName := filepath.Base(relPath)

		envPkgAliases := make(map[string]string)
		fmtPkgAliases := make(map[string]bool)
		hasDotEnvPkg := false
		hasDotFmtPkg := false

		for _, imp := range fullAST.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			localName := filepath.Base(impPath)
			if imp.Name != nil {
				localName = imp.Name.Name
			}

			if impPath == "os" || impPath == "syscall" {
				if localName == "." {
					hasDotEnvPkg = true
				} else if localName != "_" {
					envPkgAliases[localName] = impPath
				}
			}
			if impPath == "fmt" {
				if localName == "." {
					hasDotFmtPkg = true
				} else if localName != "_" {
					fmtPkgAliases[localName] = true
				}
			}
		}

		// 1. Core Independence: internal/core НІКОЛИ не імпортує internal/modules/*
		if strings.HasPrefix(relPath, "internal/core/") {
			for _, imp := range fullAST.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)
				if strings.Contains(impPath, "internal/modules") {
					violations = append(violations, "[CORE->MODULE] "+relPath+": internal/core MUST NEVER import module package "+impPath)
				}
			}
		}

		// 2. Перевірка модулів internal/modules/<mod>/...
		var currentModule string
		if strings.HasPrefix(relPath, "internal/modules/") {
			parts := strings.Split(relPath, "/")
			if len(parts) >= 3 {
				currentModule = parts[2]
			}
		}

		if currentModule != "" {
			if fileName == "service.go" {
				testPath := filepath.Join(filepath.Dir(path), "service_test.go")
				if _, statErr := os.Stat(testPath); os.IsNotExist(statErr) {
					violations = append(violations, "[MISSING TEST] "+relPath+": module "+currentModule+" has service.go but is missing service_test.go")
				}
			}

			isService := strings.HasPrefix(fileName, "service")
			isHandler := strings.Contains(fileName, "handler")
			isRepo := strings.HasPrefix(fileName, "repo")
			isModel := strings.HasPrefix(fileName, "model")

			for _, imp := range fullAST.Imports {
				impPath := strings.Trim(imp.Path.Value, `"`)

				if idx := strings.Index(impPath, "internal/modules/"); idx != -1 {
					rest := impPath[idx+len("internal/modules/"):]
					targetModule := strings.Split(rest, "/")[0]
					if targetModule != "" && targetModule != currentModule {
						violations = append(violations, "[CROSS-MODULE] "+relPath+": module "+currentModule+" MUST NEVER import another module "+targetModule)
					}
				}

				if isService && (isSQLPackage(impPath) || isHTTPOrBotPackage(impPath)) {
					violations = append(violations, "[LAYER-SERVICE] "+relPath+": service file MUST NOT import "+impPath)
				}
				if isHandler && isSQLPackage(impPath) {
					violations = append(violations, "[LAYER-HANDLER] "+relPath+": handler file MUST NOT import SQL package "+impPath)
				}
				if isRepo && isHTTPOrBotPackage(impPath) {
					violations = append(violations, "[LAYER-REPO] "+relPath+": repository file MUST NOT import HTTP/Bot package "+impPath)
				}
			}

			if isHandler {
				forbiddenRepoIdents := map[string]bool{
					"Repository":          true,
					"SQLiteRepository":    true,
					"NewRepository":       true,
					"NewSQLiteRepository": true,
				}
				ast.Inspect(fullAST, func(n ast.Node) bool {
					if ident, ok := n.(*ast.Ident); ok && forbiddenRepoIdents[ident.Name] {
						violations = append(violations, "[HANDLER->REPO BYPASS] "+relPath+": handler MUST NOT reference "+ident.Name+" directly (must call Service)")
					}
					return true
				})
			}

			if isModel {
				violations = append(violations, checkMoneyFloatsInAST(relPath, fullAST)...)
			}

			violations = append(violations, checkModuleSQLRules(relPath, currentModule, isRepo, fullAST)...)
		}

		// 3. Глобальні AST-перевірки для всіх продакшн .go файлів
		forbiddenEnvFuncs := map[string]bool{
			"Getenv":    true,
			"LookupEnv": true,
			"Environ":   true,
			"ExpandEnv": true,
			"Expand":    true,
		}
		forbiddenFmtFuncs := map[string]bool{
			"Print":   true,
			"Println": true,
			"Printf":  true,
		}

		ast.Inspect(fullAST, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				pkgIdent, ok := node.X.(*ast.Ident)
				if !ok {
					return true
				}
				if relPath != "internal/core/config.go" {
					if origPkg, isAlias := envPkgAliases[pkgIdent.Name]; isAlias && forbiddenEnvFuncs[node.Sel.Name] {
						violations = append(violations, "[ENV-OUTSIDE-CONFIG] "+relPath+": "+origPkg+"."+node.Sel.Name+" is forbidden outside internal/core/config.go")
					}
				}
				if fmtPkgAliases[pkgIdent.Name] && forbiddenFmtFuncs[node.Sel.Name] {
					violations = append(violations, "[FMT-PRINT] "+relPath+": fmt."+node.Sel.Name+" is forbidden in production code (use log/slog)")
				}

			case *ast.Ident:
				if relPath != "internal/core/config.go" && hasDotEnvPkg && forbiddenEnvFuncs[node.Name] {
					violations = append(violations, "[ENV-OUTSIDE-CONFIG] "+relPath+": dot-imported "+node.Name+" is forbidden outside internal/core/config.go")
				}
				if hasDotFmtPkg && forbiddenFmtFuncs[node.Name] {
					violations = append(violations, "[FMT-PRINT] "+relPath+": dot-imported "+node.Name+" is forbidden in production code (use log/slog)")
				}

			case *ast.CallExpr:
				if isFmtSprintfCall(node, fmtPkgAliases, hasDotFmtPkg) && len(node.Args) > 0 {
					if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						unq, _ := strconv.Unquote(lit.Value)
						if sqlDMLKeywordRe.MatchString(unq) {
							violations = append(violations, "[SQL-SPRINTF] "+relPath+": building SQL with fmt.Sprintf is forbidden (use ? parameters)")
						}
					}
				}

			case *ast.BasicLit:
				if node.Kind == token.STRING {
					val, err := strconv.Unquote(node.Value)
					if err != nil {
						return true
					}
					if relPath != "internal/core/errors.go" && strings.HasPrefix(val, "ERR_") {
						violations = append(violations, "[ADHOC-ERR-CODE] "+relPath+": error code literal "+val+" must be defined only in internal/core/errors.go")
					}
					if selectStarRe.MatchString(val) {
						violations = append(violations, "[SQL-SELECT-STAR] "+relPath+": SELECT * is forbidden (list columns explicitly)")
					}
					if createTableFloatRe.MatchString(val) {
						violations = append(violations, "[SQL-FLOAT] "+relPath+": FLOAT/REAL/DOUBLE in SQL schema is forbidden (use INTEGER minor units)")
					}
				}
			}
			return true
		})

		return nil
	})

	if walkErr != nil {
		violations = append(violations, "walk error: "+walkErr.Error())
	}
	return violations
}

func isSQLPackage(impPath string) bool {
	return impPath == "database/sql" ||
		strings.HasPrefix(impPath, "database/sql/") ||
		strings.Contains(impPath, "modernc.org/sqlite")
}

func isHTTPOrBotPackage(impPath string) bool {
	return impPath == "net/http" ||
		strings.HasPrefix(impPath, "net/http/") ||
		strings.Contains(impPath, "go-chi") ||
		strings.Contains(impPath, "telebot")
}

func isFmtSprintfCall(call *ast.CallExpr, fmtAliases map[string]bool, hasDotFmt bool) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok && fmtAliases[pkg.Name] && fun.Sel.Name == "Sprintf" {
			return true
		}
	case *ast.Ident:
		if hasDotFmt && fun.Name == "Sprintf" {
			return true
		}
	}
	return false
}

var (
	sqlDMLKeywordRe    = regexp.MustCompile(`(?i)\b(SELECT|INSERT\s+INTO|UPDATE|DELETE\s+FROM)\b`)
	selectStarRe       = regexp.MustCompile(`(?i)\bSELECT\s+\*\s+FROM\b`)
	createTableRe      = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_]+)`)
	createTableFloatRe = regexp.MustCompile(`(?i)\bCREATE\s+TABLE\b[\s\S]*?\b(FLOAT|REAL|DOUBLE)\b`)
)

// splitCamelWords розбиває CamelCase / snake_case ідентифікатор на слова в нижньому регістрі.
func splitCamelWords(ident string) []string {
	var words []string
	var cur strings.Builder
	runes := []rune(ident)
	for i, r := range runes {
		if r == '_' {
			if cur.Len() > 0 {
				words = append(words, strings.ToLower(cur.String()))
				cur.Reset()
			}
			continue
		}
		if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(runes[i-1]) && cur.Len() > 0 {
			words = append(words, strings.ToLower(cur.String()))
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		words = append(words, strings.ToLower(cur.String()))
	}
	return words
}

func isMoneyFieldName(name string) bool {
	words := splitCamelWords(name)
	moneyWords := map[string]bool{
		"amount":  true,
		"balance": true,
		"price":   true,
		"cost":    true,
		"sum":     true,
		"total":   true,
		"income":  true,
		"expense": true,
	}
	nonMoneyQualifiers := map[string]bool{
		"percent": true,
		"pct":     true,
		"ratio":   true,
		"share":   true,
		"rate":    true,
		"count":   true,
	}

	hasMoney := false
	for _, w := range words {
		if nonMoneyQualifiers[w] {
			return false
		}
		singular := strings.TrimSuffix(w, "s")
		if moneyWords[w] || moneyWords[singular] {
			hasMoney = true
		}
	}
	return hasMoney
}

// unwrapFloatType перевіряє, чи тип є float32/float64 (включно з *float64, []float64, [N]float64).
func unwrapFloatType(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		if t.Name == "float64" || t.Name == "float32" {
			return t.Name, true
		}
	case *ast.StarExpr:
		return unwrapFloatType(t.X)
	case *ast.ArrayType:
		return unwrapFloatType(t.Elt)
	case *ast.ParenExpr:
		return unwrapFloatType(t.X)
	}
	return "", false
}

func checkMoneyFloatsInAST(relPath string, f *ast.File) []string {
	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		st, ok := n.(*ast.StructType)
		if !ok || st.Fields == nil {
			return true
		}
		for _, field := range st.Fields.List {
			floatType, isFloat := unwrapFloatType(field.Type)
			if !isFloat {
				continue
			}
			for _, name := range field.Names {
				if isMoneyFieldName(name.Name) {
					violations = append(violations, "[MONEY-FLOAT] "+relPath+": money field "+name.Name+" MUST NOT use "+floatType+" (use int64 minor units)")
				}
			}
		}
		return true
	})
	return violations
}

// evalConcatStringExpr збирає повний рядок із конкатенації строкових літералів (`"..." + "..."`).
func evalConcatStringExpr(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		val, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return val, true
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, okL := evalConcatStringExpr(e.X)
		right, okR := evalConcatStringExpr(e.Y)
		if okL && okR {
			return left + right, true
		}
	case *ast.ParenExpr:
		return evalConcatStringExpr(e.X)
	}
	return "", false
}

func checkModuleSQLRules(relPath, moduleName string, isRepo bool, f *ast.File) []string {
	var violations []string
	prefix, known := knownModuleTablePrefixes[moduleName]
	if !known {
		return []string{"[UNKNOWN-MODULE-PREFIX] " + relPath + ": module " + moduleName + " has no registered table prefix in knownModuleTablePrefixes (SPEC.md)"}
	}

	// Збираємо всі верхньорівневі строкові вирази (окремі літерали та ланцюжки конкатенації +),
	// щоб не дублювати перевірку для вкладених частин BinaryExpr.
	var inspectTopLevelStrings func(n ast.Node)
	inspectTopLevelStrings = func(n ast.Node) {
		if n == nil {
			return
		}
		if bin, ok := n.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
			if fullStr, ok := evalConcatStringExpr(bin); ok {
				violations = append(violations, validateModuleSQLString(relPath, moduleName, prefix, isRepo, fullStr)...)
				return
			}
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if val, err := strconv.Unquote(lit.Value); err == nil {
				violations = append(violations, validateModuleSQLString(relPath, moduleName, prefix, isRepo, val)...)
			}
			return
		}

		ast.Inspect(n, func(child ast.Node) bool {
			if child == nil || child == n {
				return true
			}
			if bin, ok := child.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
				if fullStr, ok := evalConcatStringExpr(bin); ok {
					violations = append(violations, validateModuleSQLString(relPath, moduleName, prefix, isRepo, fullStr)...)
					return false
				}
			}
			if lit, ok := child.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if val, err := strconv.Unquote(lit.Value); err == nil {
					violations = append(violations, validateModuleSQLString(relPath, moduleName, prefix, isRepo, val)...)
				}
				return false
			}
			return true
		})
	}
	inspectTopLevelStrings(f)

	return violations
}

func validateModuleSQLString(relPath, moduleName, prefix string, isRepo bool, val string) []string {
	var violations []string
	for _, m := range createTableRe.FindAllStringSubmatch(val, -1) {
		if len(m) > 1 {
			tableName := strings.ToLower(m[1])
			if !strings.HasPrefix(tableName, prefix) {
				violations = append(violations, "[TABLE-PREFIX] "+relPath+": table "+tableName+" in module "+moduleName+" MUST start with prefix "+prefix)
			}
		}
	}

	if isRepo && sqlDMLKeywordRe.MatchString(val) && strings.Contains(strings.ToLower(val), prefix) {
		if !strings.Contains(strings.ToLower(val), "workspace_id") {
			violations = append(violations, "[MISSING-WORKSPACE-ID] "+relPath+": module SQL query on "+prefix+"* MUST filter by workspace_id")
		}
	}
	return violations
}

var (
	feImportSpecRe    = regexp.MustCompile(`(?:from\s+|import\s*\(?\s*|require\s*\(\s*)['"]([^'"]+)['"]`)
	feDirectNetworkRe = regexp.MustCompile(`\b(fetch|axios|indexedDB)\b`)
)

func checkFrontendArchitecture(rootDir string) []string {
	var violations []string
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return []string{"resolve rootDir: " + err.Error()}
	}

	webSrc := filepath.Join(absRoot, "web", "src")
	if _, statErr := os.Stat(webSrc); os.IsNotExist(statErr) {
		return nil
	}

	walkErr := filepath.WalkDir(webSrc, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") {
			return nil
		}

		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return relErr
		}
		relPath := filepath.ToSlash(rel)

		rawBytes, readErr := os.ReadFile(path)
		if readErr != nil {
			violations = append(violations, "read "+relPath+": "+readErr.Error())
			return nil
		}
		cleanContent := stripJSComments(string(rawBytes))

		// 1. Заборона import.meta.env поза web/src/core/config.ts
		if relPath != "web/src/core/config.ts" && !strings.HasSuffix(relPath, ".d.ts") {
			if strings.Contains(cleanContent, "import.meta.env") {
				violations = append(violations, "[FE-ENV-OUTSIDE-CONFIG] "+relPath+": import.meta.env is forbidden outside web/src/core/config.ts")
			}
		}

		// 2. Перевірка імпортів (тільки реальні шляхи web/src/modules/<name> або відомі alias)
		isCoreFile := strings.HasPrefix(relPath, "web/src/core/")
		var currentModule string
		if strings.HasPrefix(relPath, "web/src/modules/") {
			parts := strings.Split(relPath, "/")
			if len(parts) >= 4 {
				currentModule = parts[3]
			}
		}

		for _, m := range feImportSpecRe.FindAllStringSubmatch(cleanContent, -1) {
			if len(m) < 2 {
				continue
			}
			targetModule := resolveFrontendTargetModule(relPath, m[1])
			if targetModule == "" {
				continue
			}
			if isCoreFile {
				violations = append(violations, "[FE-CORE->MODULE] "+relPath+": web/src/core MUST NEVER import from module "+targetModule)
			} else if currentModule != "" && targetModule != currentModule {
				violations = append(violations, "[FE-CROSS-MODULE] "+relPath+": frontend module "+currentModule+" MUST NEVER import from module "+targetModule)
			}
		}

		// 3. Перевірка View-шару (components/*.tsx та *Page.tsx) на прямі fetch/axios/indexedDB
		isViewFile := (strings.Contains(relPath, "/components/") && strings.HasSuffix(relPath, ".tsx")) ||
			strings.HasSuffix(relPath, "Page.tsx")
		if isViewFile && feDirectNetworkRe.MatchString(cleanContent) {
			violations = append(violations, "[FE-VIEW-NETWORK] "+relPath+": React view components/pages MUST NEVER use fetch, axios, or indexedDB directly")
		}

		return nil
	})

	if walkErr != nil {
		violations = append(violations, "walk frontend error: "+walkErr.Error())
	}
	return violations
}

// resolveFrontendTargetModule визначає модуль тільки для реальних шляхів web/src/modules/<name> або відомих alias.
func resolveFrontendTargetModule(fromRelPath, importSpec string) string {
	var resolved string
	switch {
	case strings.HasPrefix(importSpec, "."):
		resolved = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(fromRelPath), importSpec)))
	case strings.HasPrefix(importSpec, "@/modules/"):
		resolved = "web/src/" + strings.TrimPrefix(importSpec, "@/")
	case strings.HasPrefix(importSpec, "src/modules/"):
		resolved = "web/" + importSpec
	case strings.HasPrefix(importSpec, "web/src/modules/"):
		resolved = importSpec
	default:
		return ""
	}

	if !strings.HasPrefix(resolved, "web/src/modules/") {
		return ""
	}
	rest := strings.TrimPrefix(resolved, "web/src/modules/")
	modName := strings.Split(rest, "/")[0]
	return modName
}

// stripJSComments видаляє // та /* */ коментарі, не дозволяючи апострофам у JSX-тексті поглинати наступний код.
func stripJSComments(src string) string {
	var out strings.Builder
	out.Grow(len(src))

	n := len(src)
	i := 0
	for i < n {
		c := src[i]
		if c == '\'' || c == '"' || c == '`' {
			// Англійський апостроф після літери (Don't, user's) у JSX не відкриває JS-рядок
			if c == '\'' && i > 0 && isASCIILetter(src[i-1]) {
				out.WriteByte(c)
				i++
				continue
			}

			quote := c
			out.WriteByte(c)
			i++
			for i < n {
				ch := src[i]
				// Звичайні лапки ' і " не перетинають перенесення рядка без екранування
				if (quote == '\'' || quote == '"') && ch == '\n' {
					out.WriteByte(ch)
					i++
					break
				}
				out.WriteByte(ch)
				i++
				if ch == '\\' && i < n {
					out.WriteByte(src[i])
					i++
					continue
				}
				if ch == quote {
					break
				}
			}
			continue
		}

		if c == '/' && i+1 < n && src[i+1] == '/' {
			i += 2
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}

		if c == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}

		out.WriteByte(c)
		i++
	}
	return out.String()
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// TestArchitectureValidatorCatchesViolations перевіряє сам валідатор архітектури на негативних та позитивних фікстурах.
func TestArchitectureValidatorCatchesViolations(t *testing.T) {
	t.Run("GoFixtures", func(t *testing.T) {
		cases := []struct {
			name        string
			relFile     string
			code        string
			expectToken string // порожній рядок = очікуємо 0 порушень (позитивна фікстура)
		}{
			{
				name:        "core imports module",
				relFile:     "internal/core/bad.go",
				code:        `package core; import _ "kasabot/internal/modules/finance"`,
				expectToken: "[CORE->MODULE]",
			},
			{
				name:        "module A imports module B",
				relFile:     "internal/modules/finance/repo_extra.go",
				code:        `package finance; import _ "kasabot/internal/modules/orders"`,
				expectToken: "[CROSS-MODULE]",
			},
			{
				name:        "service_x.go imports net/http/httputil",
				relFile:     "internal/modules/finance/service_helper.go",
				code:        `package finance; import _ "net/http/httputil"`,
				expectToken: "[LAYER-SERVICE]",
			},
			{
				name:        "handler references Repository in same package without import",
				relFile:     "internal/modules/finance/http_handler.go",
				code:        `package finance; type Handler struct { repo Repository }`,
				expectToken: "[HANDLER->REPO BYPASS]",
			},
			{
				name:        "service.go without service_test.go",
				relFile:     "internal/modules/finance/service.go",
				code:        `package finance; type Service struct{}`,
				expectToken: "[MISSING TEST]",
			},
			{
				name:        "os.ExpandEnv via alias outside config.go",
				relFile:     "internal/core/other.go",
				code:        `package core; import o "os"; var _ = o.ExpandEnv("$HOME")`,
				expectToken: "[ENV-OUTSIDE-CONFIG]",
			},
			{
				name:        "fmt.Println in production code",
				relFile:     "internal/core/other.go",
				code:        `package core; import "fmt"; func f() { fmt.Println("hi") }`,
				expectToken: "[FMT-PRINT]",
			},
			{
				name:        "ad-hoc ERR_ code outside errors.go",
				relFile:     "internal/core/other.go",
				code:        `package core; const bad = "ERR_CUSTOM_ADHOC"`,
				expectToken: "[ADHOC-ERR-CODE]",
			},
			{
				name:        "pointer money field uses *float64 in model.go",
				relFile:     "internal/modules/finance/model.go",
				code:        `package finance; type Tx struct { Balance *float64 }`,
				expectToken: "[MONEY-FLOAT]",
			},
			{
				name:        "slice money field uses []float64 in model.go",
				relFile:     "internal/modules/finance/model.go",
				code:        `package finance; type Tx struct { Amounts []float64 }`,
				expectToken: "[MONEY-FLOAT]",
			},
			{
				name:        "Summary and TotalPercent float64 are allowed",
				relFile:     "internal/modules/finance/model.go",
				code:        `package finance; type Stats struct { Summary float64; TotalPercent float64; AmountMinor int64 }`,
				expectToken: "",
			},
			{
				name:        "table created with wrong prefix in finance module",
				relFile:     "internal/modules/finance/module.go",
				code:        `package finance; const q = "CREATE TABLE transactions (id INT)"`,
				expectToken: "[TABLE-PREFIX]",
			},
			{
				name:        "unknown module without registered table prefix fails",
				relFile:     "internal/modules/billing/model.go",
				code:        `package billing; type Plan struct { ID int64 }`,
				expectToken: "[UNKNOWN-MODULE-PREFIX]",
			},
			{
				name:        "SELECT * in query",
				relFile:     "internal/modules/finance/repository.go",
				code:        `package finance; const q = "SELECT * FROM fin_transactions WHERE workspace_id = ?"`,
				expectToken: "[SQL-SELECT-STAR]",
			},
			{
				name:        "concatenated SQL query in repository missing workspace_id",
				relFile:     "internal/modules/finance/repository.go",
				code:        `package finance; const q = "SELECT id, amount_minor " + "FROM fin_transactions " + "WHERE id = ?"`,
				expectToken: "[MISSING-WORKSPACE-ID]",
			},
			{
				name:        "concatenated SQL query with workspace_id passes",
				relFile:     "internal/modules/finance/repository.go",
				code:        `package finance; const q = "SELECT id, amount_minor " + "FROM fin_transactions " + "WHERE workspace_id = ?"`,
				expectToken: "",
			},
			{
				name:        "SQL built with fmt.Sprintf",
				relFile:     "internal/modules/finance/repository.go",
				code:        `package finance; import "fmt"; func q(w int64) string { return fmt.Sprintf("SELECT id FROM fin_transactions WHERE workspace_id = %d", w) }`,
				expectToken: "[SQL-SPRINTF]",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				tmp := t.TempDir()
				fullPath := filepath.Join(tmp, filepath.FromSlash(tc.relFile))
				if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(fullPath, []byte(tc.code), 0600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}

				got := checkGoArchitecture(tmp)
				joined := strings.Join(got, "\n")
				if tc.expectToken == "" {
					if len(got) > 0 {
						t.Fatalf("expected 0 violations, got:\n%s", joined)
					}
					return
				}
				if !strings.Contains(joined, tc.expectToken) {
					t.Fatalf("expected violation containing %q, got:\n%s", tc.expectToken, joined)
				}
			})
		}
	})

	t.Run("FrontendFixtures", func(t *testing.T) {
		cases := []struct {
			name        string
			relFile     string
			code        string
			expectToken string
		}{
			{
				name:        "relative cross-module import ../orders/api",
				relFile:     "web/src/modules/finance/FinancePage.tsx",
				code:        `import { x } from '../orders/api'; export const P = () => null;`,
				expectToken: "[FE-CROSS-MODULE]",
			},
			{
				name:        "alias cross-module import @/modules/orders/api",
				relFile:     "web/src/modules/finance/FinancePage.tsx",
				code:        `import { x } from '@/modules/orders/api'; export const P = () => null;`,
				expectToken: "[FE-CROSS-MODULE]",
			},
			{
				name:        "third-party package containing modules/ is allowed",
				relFile:     "web/src/modules/finance/FinancePage.tsx",
				code:        `import { x } from 'some-lib/modules/helper'; export const P = () => <div>{x}</div>;`,
				expectToken: "",
			},
			{
				name:        "frontend core imports from module",
				relFile:     "web/src/core/layout/AppLayout.tsx",
				code:        `import { x } from '../../modules/finance/api'; export const L = () => null;`,
				expectToken: "[FE-CORE->MODULE]",
			},
			{
				name:        "axios in Page.tsx view file",
				relFile:     "web/src/modules/finance/FinancePage.tsx",
				code:        `export const FinancePage = () => { axios.get('/api'); return null; };`,
				expectToken: "[FE-VIEW-NETWORK]",
			},
			{
				name:        "apostrophe in JSX text does not hide subsequent fetch()",
				relFile:     "web/src/modules/finance/components/Bad.tsx",
				code:        "export const Bad = () => {\n  const el = <span>Don't click</span>;\n  fetch('/api/steal');\n  return el;\n};\n",
				expectToken: "[FE-VIEW-NETWORK]",
			},
			{
				name:        "import.meta.env outside config.ts",
				relFile:     "web/src/modules/finance/api.ts",
				code:        `export const url = import.meta.env.VITE_API;`,
				expectToken: "[FE-ENV-OUTSIDE-CONFIG]",
			},
			{
				name:        "commented fetch or indexedDB does not trigger false positive",
				relFile:     "web/src/modules/finance/components/Clean.tsx",
				code:        "// fetch('/api') and indexedDB.open('db') are not called here\n/* axios.get('/x') */\nexport const Clean = () => <div>{t('finance.undo')}</div>;\n",
				expectToken: "",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				tmp := t.TempDir()
				fullPath := filepath.Join(tmp, filepath.FromSlash(tc.relFile))
				if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(fullPath, []byte(tc.code), 0600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}

				got := checkFrontendArchitecture(tmp)
				joined := strings.Join(got, "\n")
				if tc.expectToken == "" {
					if len(got) > 0 {
						t.Fatalf("expected 0 violations, got:\n%s", joined)
					}
					return
				}
				if !strings.Contains(joined, tc.expectToken) {
					t.Fatalf("expected violation containing %q, got:\n%s", tc.expectToken, joined)
				}
			})
		}
	})
}
