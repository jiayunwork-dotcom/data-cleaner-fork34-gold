package dsl

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type TokenType int

const (
	TokenEOF TokenType = iota
	TokenNumber
	TokenString
	TokenIdent
	TokenAnd
	TokenOr
	TokenNot
	TokenGE
	TokenLE
	TokenGT
	TokenLT
	TokenEQ
	TokenNE
	TokenPlus
	TokenMinus
	TokenStar
	TokenSlash
	TokenLParen
	TokenRParen
	TokenMatches
	TokenComma
)

type Token struct {
	Type  TokenType
	Value string
}

var keywords = map[string]TokenType{
	"and":      TokenAnd,
	"or":       TokenOr,
	"not":      TokenNot,
	"matches":  TokenMatches,
	"length":   TokenIdent,
	"contains": TokenIdent,
	"startswith": TokenIdent,
	"sum":      TokenIdent,
	"count":    TokenIdent,
	"avg":      TokenIdent,
	"abs":      TokenIdent,
	"round":    TokenIdent,
}

var allowedFunctions = map[string]bool{
	"length":     true,
	"contains":   true,
	"startswith": true,
	"sum":        true,
	"count":      true,
	"avg":        true,
	"abs":        true,
	"round":      true,
}

type Lexer struct {
	input  string
	pos    int
	tokens []Token
}

func NewLexer(input string) *Lexer {
	return &Lexer{input: input, pos: 0}
}

func (l *Lexer) Tokenize() ([]Token, error) {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			l.pos++
		case ch == '\'':
			s, err := l.readString()
			if err != nil {
				return nil, err
			}
			l.tokens = append(l.tokens, Token{Type: TokenString, Value: s})
		case ch >= '0' && ch <= '9':
			n := l.readNumber()
			l.tokens = append(l.tokens, Token{Type: TokenNumber, Value: n})
		case (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_':
			ident := l.readIdent()
			if tt, ok := keywords[strings.ToLower(ident)]; ok {
				l.tokens = append(l.tokens, Token{Type: tt, Value: ident})
			} else {
				l.tokens = append(l.tokens, Token{Type: TokenIdent, Value: ident})
			}
		case ch == '>':
			l.tokens = append(l.tokens, Token{Type: TokenGT, Value: ">"})
			l.pos++
		case ch == '>' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '=':
			l.tokens = append(l.tokens, Token{Type: TokenGE, Value: ">="})
			l.pos += 2
		case ch == '<' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '=':
			l.tokens = append(l.tokens, Token{Type: TokenLE, Value: "<="})
			l.pos += 2
		case ch == '!' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '=':
			l.tokens = append(l.tokens, Token{Type: TokenNE, Value: "!="})
			l.pos += 2
		case ch == '=' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '=':
			l.tokens = append(l.tokens, Token{Type: TokenEQ, Value: "=="})
			l.pos += 2
		case ch == '<':
			l.tokens = append(l.tokens, Token{Type: TokenLT, Value: "<"})
			l.pos++
		case ch == '=':
			l.tokens = append(l.tokens, Token{Type: TokenEQ, Value: "="})
			l.pos++
		case ch == '+':
			l.tokens = append(l.tokens, Token{Type: TokenPlus, Value: "+"})
			l.pos++
		case ch == '-':
			l.tokens = append(l.tokens, Token{Type: TokenMinus, Value: "-"})
			l.pos++
		case ch == '*':
			l.tokens = append(l.tokens, Token{Type: TokenStar, Value: "*"})
			l.pos++
		case ch == '/':
			l.tokens = append(l.tokens, Token{Type: TokenSlash, Value: "/"})
			l.pos++
		case ch == '(':
			l.tokens = append(l.tokens, Token{Type: TokenLParen, Value: "("})
			l.pos++
		case ch == ')':
			l.tokens = append(l.tokens, Token{Type: TokenRParen, Value: ")"})
			l.pos++
		case ch == ',':
			l.tokens = append(l.tokens, Token{Type: TokenComma, Value: ","})
			l.pos++
		default:
			return nil, fmt.Errorf("unexpected character '%c' at position %d", ch, l.pos)
		}
	}
	l.tokens = append(l.tokens, Token{Type: TokenEOF})
	return l.tokens, nil
}

func (l *Lexer) readString() (string, error) {
	l.pos++
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\\' && l.pos+1 < len(l.input) {
			l.pos++
			sb.WriteByte(l.input[l.pos])
			l.pos++
			continue
		}
		if ch == '\'' {
			l.pos++
			return sb.String(), nil
		}
		sb.WriteByte(ch)
		l.pos++
	}
	return "", fmt.Errorf("unterminated string")
}

func (l *Lexer) readNumber() string {
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			sb.WriteByte(ch)
			l.pos++
		} else {
			break
		}
	}
	return sb.String()
}

func (l *Lexer) readIdent() string {
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			sb.WriteByte(ch)
			l.pos++
		} else {
			break
		}
	}
	return sb.String()
}

type ExprType int

const (
	ExprLiteral ExprType = iota
	ExprField
	ExprBinary
	ExprUnary
	ExprFuncCall
	ExprComparison
	ExprLogical
	ExprMatches
)

type Expr struct {
	Type     ExprType
	Value    interface{}
	Left     *Expr
	Right    *Expr
	Op       string
	FuncName string
	Args     []*Expr
	Field    string
}

type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(tokens []Token) *Parser {
	return &Parser{tokens: tokens, pos: 0}
}

func Parse(input string) (*Expr, error) {
	lexer := NewLexer(input)
	tokens, err := lexer.Tokenize()
	if err != nil {
		return nil, fmt.Errorf("DSL lex error: %w", err)
	}
	parser := NewParser(tokens)
	expr, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.current().Type != TokenEOF {
		return nil, fmt.Errorf("unexpected token after expression: %s", parser.current().Value)
	}
	return expr, nil
}

func (p *Parser) current() Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return Token{Type: TokenEOF}
}

func (p *Parser) advance() Token {
	t := p.current()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return t
}

func (p *Parser) parseOr() (*Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.current().Type == TokenOr {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &Expr{Type: ExprLogical, Left: left, Right: right, Op: "or"}
	}
	return left, nil
}

func (p *Parser) parseAnd() (*Expr, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.current().Type == TokenAnd {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &Expr{Type: ExprLogical, Left: left, Right: right, Op: "and"}
	}
	return left, nil
}

func (p *Parser) parseNot() (*Expr, error) {
	if p.current().Type == TokenNot {
		p.advance()
		expr, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &Expr{Type: ExprUnary, Op: "not", Right: expr}, nil
	}
	return p.parseComparison()
}

func (p *Parser) parseComparison() (*Expr, error) {
	left, err := p.parseAddSub()
	if err != nil {
		return nil, err
	}

	switch p.current().Type {
	case TokenGT, TokenLT, TokenGE, TokenLE, TokenEQ, TokenNE:
		op := p.advance().Value
		right, err := p.parseAddSub()
		if err != nil {
			return nil, err
		}
		return &Expr{Type: ExprComparison, Left: left, Right: right, Op: op}, nil
	case TokenMatches:
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &Expr{Type: ExprMatches, Left: left, Right: right}, nil
	}

	return left, nil
}

func (p *Parser) parseAddSub() (*Expr, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return nil, err
	}
	for p.current().Type == TokenPlus || p.current().Type == TokenMinus {
		op := p.advance().Value
		right, err := p.parseMulDiv()
		if err != nil {
			return nil, err
		}
		left = &Expr{Type: ExprBinary, Left: left, Right: right, Op: op}
	}
	return left, nil
}

func (p *Parser) parseMulDiv() (*Expr, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for p.current().Type == TokenStar || p.current().Type == TokenSlash {
		op := p.advance().Value
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &Expr{Type: ExprBinary, Left: left, Right: right, Op: op}
	}
	return left, nil
}

func (p *Parser) parsePrimary() (*Expr, error) {
	tok := p.current()

	switch tok.Type {
	case TokenNumber:
		p.advance()
		f, _ := strconv.ParseFloat(tok.Value, 64)
		return &Expr{Type: ExprLiteral, Value: f}, nil
	case TokenString:
		p.advance()
		return &Expr{Type: ExprLiteral, Value: tok.Value}, nil
	case TokenIdent:
		p.advance()
		if p.current().Type == TokenLParen {
			return p.parseFuncCall(tok.Value)
		}
		return &Expr{Type: ExprField, Field: tok.Value}, nil
	case TokenLParen:
		p.advance()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.current().Type != TokenRParen {
			return nil, fmt.Errorf("expected ')', got %s", p.current().Value)
		}
		p.advance()
		return expr, nil
	case TokenMinus:
		p.advance()
		expr, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &Expr{Type: ExprUnary, Op: "-", Right: expr}, nil
	default:
		return nil, fmt.Errorf("unexpected token: %s (%d)", tok.Value, tok.Type)
	}
}

func (p *Parser) parseFuncCall(name string) (*Expr, error) {
	if !allowedFunctions[name] {
		return nil, fmt.Errorf("function '%s' is not allowed; only whitelisted functions are permitted", name)
	}
	p.advance()

	var args []*Expr
	if p.current().Type != TokenRParen {
		arg, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		for p.current().Type == TokenComma {
			p.advance()
			arg, err := p.parseOr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
		}
	}

	if p.current().Type != TokenRParen {
		return nil, fmt.Errorf("expected ')' in function call, got %s", p.current().Value)
	}
	p.advance()

	return &Expr{Type: ExprFuncCall, FuncName: name, Args: args}, nil
}

type EvalContext struct {
	Row      map[string]interface{}
	AllRows  []map[string]interface{}
}

func Evaluate(expr *Expr, ctx *EvalContext) (interface{}, error) {
	switch expr.Type {
	case ExprLiteral:
		return expr.Value, nil
	case ExprField:
		val, ok := ctx.Row[expr.Field]
		if !ok {
			return nil, nil
		}
		return val, nil
	case ExprBinary:
		left, err := Evaluate(expr.Left, ctx)
		if err != nil {
			return nil, err
		}
		right, err := Evaluate(expr.Right, ctx)
		if err != nil {
			return nil, err
		}
		return evalBinary(expr.Op, left, right)
	case ExprUnary:
		val, err := Evaluate(expr.Right, ctx)
		if err != nil {
			return nil, err
		}
		return evalUnary(expr.Op, val)
	case ExprComparison:
		left, err := Evaluate(expr.Left, ctx)
		if err != nil {
			return nil, err
		}
		right, err := Evaluate(expr.Right, ctx)
		if err != nil {
			return nil, err
		}
		return evalComparison(expr.Op, left, right)
	case ExprLogical:
		left, err := Evaluate(expr.Left, ctx)
		if err != nil {
			return nil, err
		}
		right, err := Evaluate(expr.Right, ctx)
		if err != nil {
			return nil, err
		}
		return evalLogical(expr.Op, left, right)
	case ExprMatches:
		left, err := Evaluate(expr.Left, ctx)
		if err != nil {
			return nil, err
		}
		right, err := Evaluate(expr.Right, ctx)
		if err != nil {
			return nil, err
		}
		leftStr := fmt.Sprintf("%v", left)
		pattern, ok := right.(string)
		if !ok {
			return false, fmt.Errorf("matches requires string pattern")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, fmt.Errorf("invalid pattern: %w", err)
		}
		return re.MatchString(leftStr), nil
	case ExprFuncCall:
		return evalFuncCall(expr.FuncName, expr.Args, ctx)
	default:
		return nil, fmt.Errorf("unknown expression type: %d", expr.Type)
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case nil:
		return 0, false
	default:
		f, err := strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
		return f, err == nil
	}
}

func evalBinary(op string, left, right interface{}) (interface{}, error) {
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if !lok || !rok {
		return nil, nil
	}
	switch op {
	case "+":
		return lf + rf, nil
	case "-":
		return lf - rf, nil
	case "*":
		return lf * rf, nil
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return lf / rf, nil
	default:
		return nil, fmt.Errorf("unknown binary op: %s", op)
	}
}

func evalUnary(op string, val interface{}) (interface{}, error) {
	switch op {
	case "not":
		return !toBool(val), nil
	case "-":
		f, ok := toFloat(val)
		if !ok {
			return nil, nil
		}
		return -f, nil
	default:
		return nil, fmt.Errorf("unknown unary op: %s", op)
	}
}

func evalComparison(op string, left, right interface{}) (interface{}, error) {
	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if !lok || !rok {
		ls := fmt.Sprintf("%v", left)
		rs := fmt.Sprintf("%v", right)
		switch op {
		case "==", "=":
			return ls == rs, nil
		case "!=":
			return ls != rs, nil
		default:
			return false, nil
		}
	}
	switch op {
	case ">":
		return lf > rf, nil
	case "<":
		return lf < rf, nil
	case ">=":
		return lf >= rf, nil
	case "<=":
		return lf <= rf, nil
	case "==", "=":
		return lf == rf, nil
	case "!=":
		return lf != rf, nil
	default:
		return false, fmt.Errorf("unknown comparison op: %s", op)
	}
}

func evalLogical(op string, left, right interface{}) (interface{}, error) {
	switch op {
	case "and":
		return toBool(left) && toBool(right), nil
	case "or":
		return toBool(left) || toBool(right), nil
	default:
		return false, fmt.Errorf("unknown logical op: %s", op)
	}
}

func evalFuncCall(name string, args []*Expr, ctx *EvalContext) (interface{}, error) {
	switch name {
	case "length":
		if len(args) == 0 {
			return 0, nil
		}
		val, err := Evaluate(args[0], ctx)
		if err != nil {
			return nil, err
		}
		s := fmt.Sprintf("%v", val)
		return float64(len(s)), nil
	case "contains":
		if len(args) < 2 {
			return false, nil
		}
		val, err := Evaluate(args[0], ctx)
		if err != nil {
			return nil, err
		}
		sub, err := Evaluate(args[1], ctx)
		if err != nil {
			return nil, err
		}
		return strings.Contains(fmt.Sprintf("%v", val), fmt.Sprintf("%v", sub)), nil
	case "startswith":
		if len(args) < 2 {
			return false, nil
		}
		val, err := Evaluate(args[0], ctx)
		if err != nil {
			return nil, err
		}
		prefix, err := Evaluate(args[1], ctx)
		if err != nil {
			return nil, err
		}
		return strings.HasPrefix(fmt.Sprintf("%v", val), fmt.Sprintf("%v", prefix)), nil
	case "abs":
		if len(args) == 0 {
			return 0, nil
		}
		val, err := Evaluate(args[0], ctx)
		if err != nil {
			return nil, err
		}
		f, ok := toFloat(val)
		if !ok {
			return 0, nil
		}
		return math.Abs(f), nil
	case "round":
		if len(args) == 0 {
			return 0, nil
		}
		val, err := Evaluate(args[0], ctx)
		if err != nil {
			return nil, err
		}
		f, ok := toFloat(val)
		if !ok {
			return 0, nil
		}
		places := 0.0
		if len(args) > 1 {
			p, err := Evaluate(args[1], ctx)
			if err == nil {
				if pf, ok := toFloat(p); ok {
					places = pf
				}
			}
		}
		mult := math.Pow(10, places)
		return math.Round(f*mult) / mult, nil
	case "sum":
		if len(args) == 0 {
			return 0, nil
		}
		total := 0.0
		for _, arg := range args {
			if arg.Type == ExprField && ctx.AllRows != nil {
				for _, row := range ctx.AllRows {
					if v, ok := row[arg.Field]; ok {
						if f, ok := toFloat(v); ok {
							total += f
						}
					}
				}
			} else {
				val, err := Evaluate(arg, ctx)
				if err == nil {
					if f, ok := toFloat(val); ok {
						total += f
					}
				}
			}
		}
		return total, nil
	case "count":
		if len(args) == 0 {
			return float64(len(ctx.AllRows)), nil
		}
		fieldExpr := args[0]
		if fieldExpr.Type == ExprField && ctx.AllRows != nil {
			count := 0
			for _, row := range ctx.AllRows {
				if v, ok := row[fieldExpr.Field]; ok && v != nil {
					count++
				}
			}
			return float64(count), nil
		}
		return float64(len(ctx.AllRows)), nil
	case "avg":
		if len(args) == 0 {
			return 0, nil
		}
		total := 0.0
		count := 0
		for _, arg := range args {
			if arg.Type == ExprField && ctx.AllRows != nil {
				for _, row := range ctx.AllRows {
					if v, ok := row[arg.Field]; ok {
						if f, ok := toFloat(v); ok {
							total += f
							count++
						}
					}
				}
			}
		}
		if count == 0 {
			return 0, nil
		}
		return total / float64(count), nil
	default:
		return nil, fmt.Errorf("unknown function: %s", name)
	}
}

func toBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	case int:
		return val != 0
	case int64:
		return val != 0
	case nil:
		return false
	default:
		return fmt.Sprintf("%v", v) != ""
	}
}

func ValidateExpression(input string) error {
	_, err := Parse(input)
	return err
}
