// Package danger performs offline, advisory SQL safety analysis.
package danger

import (
	"fmt"
	"strings"

	"github.com/jd4n14/dbx/internal/sqllex"
)

type Severity string

const (
	SeveritySafe     Severity = "safe"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Finding struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Severity Severity `json:"severity"`
}

type Result struct {
	Type     string    `json:"type"`
	Safe     bool      `json:"safe"`
	Severity Severity  `json:"severity"`
	Findings []Finding `json:"findings"`
}

var writeWords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true,
	"LOAD": true, "CALL": true, "DROP": true, "TRUNCATE": true,
	"ALTER": true, "CREATE": true, "RENAME": true, "GRANT": true,
	"REVOKE": true, "SET": true, "LOCK": true, "UNLOCK": true,
	"HANDLER": true, "DO": true, "USE": true, "MERGE": true,
	"ANALYZE": true, "OPTIMIZE": true, "REPAIR": true, "FLUSH": true,
	"KILL": true, "BEGIN": true, "START": true, "COMMIT": true,
	"ROLLBACK": true, "SAVEPOINT": true, "RELEASE": true,
}

var statementWords = map[string]bool{
	"SELECT": true, "SHOW": true, "DESCRIBE": true, "DESC": true,
	"EXPLAIN": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"REPLACE": true, "LOAD": true, "CALL": true, "DROP": true,
	"TRUNCATE": true, "ALTER": true, "CREATE": true, "RENAME": true,
	"GRANT": true, "REVOKE": true, "SET": true, "LOCK": true,
	"UNLOCK": true, "HANDLER": true, "DO": true, "USE": true,
	"MERGE": true, "ANALYZE": true, "OPTIMIZE": true, "REPAIR": true,
	"FLUSH": true, "KILL": true, "BEGIN": true, "START": true,
	"COMMIT": true, "ROLLBACK": true, "SAVEPOINT": true, "RELEASE": true,
}

// Analyze returns an advisory result and never accesses a database. env may be
// empty, dev, staging, prod, or readonly; validation belongs to config.
func Analyze(sql, env string) (Result, error) {
	scan := sqllex.Lex(sql)
	if !scan.HasContent || len(scan.Tokens) == 0 {
		return Result{}, fmt.Errorf("SQL is empty")
	}
	r := Result{Type: "danger", Severity: SeveritySafe, Findings: []Finding{}}
	add := func(code, message string, severity Severity) {
		r.Findings = append(r.Findings, Finding{Code: code, Message: message, Severity: severity})
		if severity == SeverityCritical || severity == SeverityWarning && r.Severity == SeveritySafe {
			r.Severity = severity
		}
	}

	if sqllex.HasMultipleStatements(sql) {
		add("multiple_statements", "La entrada contiene múltiples sentencias SQL.", SeverityCritical)
	}
	verb, start := effectiveVerb(scan.Tokens)
	nonRead := writeWords[verb]

	switch verb {
	case "DROP":
		add("drop_statement", "DROP puede eliminar objetos o datos.", SeverityCritical)
	case "TRUNCATE":
		add("truncate_statement", "TRUNCATE elimina todas las filas de una tabla.", SeverityCritical)
	case "ALTER":
		add("alter_statement", "ALTER modifica la estructura de la base de datos.", SeverityCritical)
	case "CREATE":
		if createIsIndex(scan.Tokens, start+1) {
			add("create_index", "CREATE INDEX modifica la estructura y puede bloquear una tabla.", SeverityCritical)
		} else {
			add("write_statement", "La sentencia CREATE modifica la base de datos.", SeverityWarning)
		}
	case "UPDATE", "DELETE":
		if !hasTopLevel(scan.Tokens, start+1, "WHERE") {
			code := strings.ToLower(verb) + "_without_where"
			add(code, verb+" sin WHERE de nivel superior puede afectar todas las filas.", SeverityCritical)
		} else {
			add("write_statement", "La sentencia "+verb+" modifica datos.", SeverityWarning)
		}
	default:
		if nonRead {
			add("write_statement", "La sentencia "+verb+" puede modificar la base de datos.", SeverityWarning)
		} else if !isReadVerb(verb) {
			add("unrecognized_statement", "No se pudo clasificar la sentencia SQL de forma segura.", SeverityWarning)
		}
	}

	if verb == "SELECT" {
		if hasTopLevelSequence(scan.Tokens, start+1, "INTO", "OUTFILE") || hasTopLevelSequence(scan.Tokens, start+1, "INTO", "DUMPFILE") {
			add("select_into_file", "SELECT INTO OUTFILE/DUMPFILE escribe un archivo en el servidor.", SeverityWarning)
		}
		if hasTopLevelSequence(scan.Tokens, start+1, "FOR", "UPDATE") {
			add("select_for_update", "SELECT FOR UPDATE bloquea filas.", SeverityWarning)
		}
	}
	if nonRead && (env == "prod" || env == "readonly") {
		add("restricted_environment_write", "La sentencia no es de lectura en una conexión "+env+".", SeverityCritical)
	}
	r.Safe = len(r.Findings) == 0
	return r, nil
}

func isReadVerb(verb string) bool {
	return verb == "SELECT" || verb == "SHOW" || verb == "DESCRIBE" || verb == "DESC" || verb == "EXPLAIN"
}

func createIsIndex(tokens []sqllex.Token, start int) bool {
	for ; start < len(tokens); start++ {
		if tokens[start].Depth != 0 {
			continue
		}
		switch tokens[start].Word {
		case "INDEX":
			return true
		case "UNIQUE", "FULLTEXT", "SPATIAL", "OR", "REPLACE", "TEMPORARY":
			continue
		default:
			return false
		}
	}
	return false
}

func effectiveVerb(tokens []sqllex.Token) (string, int) {
	if len(tokens) == 0 {
		return "", -1
	}
	if tokens[0].Word != "WITH" {
		return tokens[0].Word, 0
	}
	for i := 1; i < len(tokens); i++ {
		if tokens[i].Depth == 0 && statementWords[tokens[i].Word] {
			return tokens[i].Word, i
		}
	}
	return "WITH", 0
}

func nextTopLevel(tokens []sqllex.Token, start int) string {
	for ; start < len(tokens); start++ {
		if tokens[start].Depth == 0 {
			return tokens[start].Word
		}
	}
	return ""
}

func hasTopLevel(tokens []sqllex.Token, start int, word string) bool {
	for ; start < len(tokens); start++ {
		if tokens[start].Depth == 0 && tokens[start].Word == word {
			return true
		}
	}
	return false
}

func hasTopLevelSequence(tokens []sqllex.Token, start int, a, b string) bool {
	for ; start < len(tokens); start++ {
		if tokens[start].Depth != 0 || tokens[start].Word != a {
			continue
		}
		return nextTopLevel(tokens, start+1) == b
	}
	return false
}
