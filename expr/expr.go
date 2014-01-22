package expr

import (
	"fmt"
	"reflect"
	"runtime"

	"github.com/StackExchange/tcollector/opentsdb"
	"github.com/StackExchange/tsaf/expr/parse"
)

type state struct {
	*Expr
	host string
}

type Expr struct {
	*parse.Tree
}

func New(expr string) (*Expr, error) {
	t, err := parse.Parse(expr, Builtins)
	if err != nil {
		return nil, err
	}
	e := &Expr{
		Tree: t,
	}
	return e, nil
}

// Execute applies a parse expression to the specified OpenTSDB host, and
// returns one result per group.
func (e *Expr) Execute(host string) (r []*Result, err error) {
	defer errRecover(&err)
	s := &state{
		e,
		host,
	}
	r = s.walk(e.Tree.Root)
	return
}

// errRecover is the handler that turns panics into returns from the top
// level of Parse.
func errRecover(errp *error) {
	e := recover()
	if e != nil {
		switch err := e.(type) {
		case runtime.Error:
			panic(e)
		case error:
			*errp = err
		default:
			panic(e)
		}
	}
}

type Value float64

type Result struct {
	Value
	Group opentsdb.TagSet
}

type Union struct {
	A, B  Value
	Group opentsdb.TagSet
}

// wrap creates a new Result with a nil group and given value.
func wrap(v float64) []*Result {
	return []*Result{
		{
			Value: Value(v),
			Group: nil,
		},
	}
}

// union returns the combination of a and b where one is a strict subset of the
// other.
func union(a, b []*Result) []Union {
	var u []Union
	for _, ra := range a {
		for _, rb := range b {
			if ra.Group.Equal(rb.Group) || len(ra.Group) == 0 || len(rb.Group) == 0 {
				g := ra.Group
				if len(ra.Group) == 0 {
					g = rb.Group
				}
				u = append(u, Union{
					A:     ra.Value,
					B:     rb.Value,
					Group: g,
				})
			} else if ra.Group.Subset(rb.Group) {
				u = append(u, Union{
					A:     ra.Value,
					B:     rb.Value,
					Group: rb.Group,
				})
			} else if rb.Group.Subset(ra.Group) {
				u = append(u, Union{
					A:     ra.Value,
					B:     rb.Value,
					Group: ra.Group,
				})
			}
		}
	}
	return u
}

func (e *state) walk(node parse.Node) []*Result {
	switch node := node.(type) {
	case *parse.BoolNode:
		return e.walk(node.Expr)
	case *parse.NumberNode:
		return wrap(node.Float64)
	case *parse.BinaryNode:
		return e.walkBinary(node)
	case *parse.UnaryNode:
		return e.walkUnary(node)
	case *parse.FuncNode:
		return e.walkFunc(node)
	default:
		panic(fmt.Errorf("expr: unknown node type"))
	}
}

func (e *state) walkBinary(node *parse.BinaryNode) []*Result {
	a := e.walk(node.Args[0])
	b := e.walk(node.Args[1])
	var res []*Result
	u := union(a, b)
	for _, v := range u {
		a := v.A
		b := v.B
		var r Value
		switch node.OpStr {
		case "+":
			r = a + b
		case "*":
			r = a * b
		case "-":
			r = a - b
		case "/":
			r = a / b
		case "==":
			if a == b {
				r = 1
			} else {
				r = 0
			}
		case ">":
			if a > b {
				r = 1
			} else {
				r = 0
			}
		case "!=":
			if a != b {
				r = 1
			} else {
				r = 0
			}
		case "<":
			if a < b {
				r = 1
			} else {
				r = 0
			}
		case ">=":
			if a >= b {
				r = 1
			} else {
				r = 0
			}
		case "<=":
			if a <= b {
				r = 1
			} else {
				r = 0
			}
		case "||":
			if a != 0 || b != 0 {
				r = 1
			} else {
				r = 0
			}
		case "&&":
			if a != 0 && b != 0 {
				r = 1
			} else {
				r = 0
			}
		default:
			panic(fmt.Errorf("expr: unknown operator %s", node.OpStr))
		}
		res = append(res, &Result{
			Value: r,
			Group: v.Group,
		})
	}
	return res
}

func (e *state) walkUnary(node *parse.UnaryNode) []*Result {
	a := e.walk(node.Arg)
	for _, r := range a {
		switch node.OpStr {
		case "!":
			if r.Value == 0 {
				r.Value = 1
			} else {
				r.Value = 0
			}
		case "-":
			r.Value = -r.Value
		default:
			panic(fmt.Errorf("expr: unknown operator %s", node.OpStr))
		}
	}
	return a
}

func (e *state) walkFunc(node *parse.FuncNode) []*Result {
	f := reflect.ValueOf(node.F.F)
	var in []reflect.Value
	for _, a := range node.Args {
		var v interface{}
		switch t := a.(type) {
		case *parse.StringNode:
			v = t.Text
		case *parse.NumberNode:
			v = t.Float64
		case *parse.QueryNode:
			v = t.Text
		default:
			panic(fmt.Errorf("expr: unknown func arg type"))
		}
		in = append(in, reflect.ValueOf(v))
	}
	ld := len(node.F.Args) - len(node.F.Defaults)
	for i, l := len(in), len(node.F.Args); i < l; i++ {
		d := node.F.Defaults[i-ld]
		in = append(in, reflect.ValueOf(d))
	}
	args := []reflect.Value{
		reflect.ValueOf(e.host),
	}
	args = append(args, in...)
	fr := f.Call(args)
	res := fr[0].Interface().([]*Result)
	if len(fr) > 1 && !fr[1].IsNil() {
		err := fr[1].Interface().(error)
		if err != nil {
			panic(err)
		}
	}
	return res
}