// Copyright 2014 Elliott Stoneham and The TARDIS Go Authors
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package pogo

import (
	"bytes"
	"errors"
	"fmt"
	"go/types"
	"os"
	"strings"
	"sync"

	"github.com/tardisgo/tardisgo/tgossa"
	"github.com/tardisgo/tardisgo/tgoutil"
	"golang.org/x/tools/go/ssa"
)

// The Language interface enables multiple target languages for TARDIS Go.
type Language interface {
	RegisterName(val ssa.Value) string
	DeclareTempVar(ssa.Value) string
	LanguageName() string
	FileTypeSuffix() string // e.g. ".go" ".js" ".hx"
	FileStart(packageName, headerText string) string
	FileEnd() string
	SetPosHash() string
	RunDefers(usesGr bool) string
	GoClassStart() string
	GoClassEnd(*ssa.Package) string
	SubFnStart(int, bool, []ssa.Instruction) string
	SubFnEnd(id int, pos int, mustSplit bool) string
	SubFnCall(int) string
	FuncName(*ssa.Function) string
	FieldAddr(register string, v interface{}, errorInfo string) string
	IndexAddr(register string, v interface{}, errorInfo string) string
	Comment(string) string
	LangName(p, o string) string
	Const(lit ssa.Const, position string) (string, string)
	NamedConst(packageName, objectName string, val ssa.Const, position string) string
	Global(packageName, objectName string, glob ssa.Global, position string, isPublic bool) string
	FuncStart(pName, mName string, fn *ssa.Function, blks []*ssa.BasicBlock, posStr string, isPublic, trackPhi, usesGr bool, canOptMap map[string]bool, reconstruct []tgossa.BlockFormat) string
	RunEnd(fn *ssa.Function) string
	FuncEnd(fn *ssa.Function) string
	BlockStart(block []*ssa.BasicBlock, num int, emitPhi bool) string
	BlockEnd(block []*ssa.BasicBlock, num int, emitPhi bool) string
	Jump(to int, from int, code string) string
	If(v interface{}, trueNext, falseNext, phi int, trueCode, falseCode, errorInfo string) string
	Phi(register string, phiEntries []int, valEntries []interface{}, defaultValue, errorInfo string) string
	LangType(types.Type, bool, string) string
	Value(v interface{}, errorInfo string) string
	BinOp(register string, regTyp types.Type, op string, v1, v2 interface{}, errorInfo string) string
	UnOp(register string, regTyp types.Type, op string, v interface{}, commaOK bool, errorInfo string) string
	Store(v1, v2 interface{}, errorInfo string) string
	Send(v1, v2 interface{}, errorInfo string) string
	Ret(values []*ssa.Value, errorInfo string) string
	RegEq(r string) string
	Call(register string, cc ssa.CallCommon, args []ssa.Value, isBuiltin, isGo, isDefer, usesGr bool, fnToCall, errorInfo string) string
	Convert(register, langType string, destType types.Type, v interface{}, errorInfo string) string
	MakeInterface(register string, regTyp types.Type, v interface{}, errorInfo string) string
	ChangeInterface(register string, regTyp types.Type, v interface{}, errorInfo string) string
	ChangeType(register string, regTyp, v interface{}, errorInfo string) string
	Alloc(register string, heap bool, v interface{}, errorInfo string) string
	MakeClosure(register string, v interface{}, errorInfo string) string
	MakeSlice(register string, v interface{}, errorInfo string) string
	MakeChan(register string, v interface{}, errorInfo string) string
	MakeMap(register string, v interface{}, errorInfo string) string
	Slice(register string, x, low, high interface{}, errorInfo string) string
	Index(register string, v1, v2 interface{}, errorInfo string) string
	RangeCheck(x, i interface{}, length int, errorInfo string) string
	Field(register string, v interface{}, fNum int, name, errorInfo string, isFunctionName bool) string
	MapUpdate(Map, Key, Value interface{}, errorInfo string) string
	Lookup(register string, Map, Key interface{}, commaOk bool, errorInfo string) string
	Extract(register string, tuple interface{}, index int, errorInfo string) string
	Range(register string, v interface{}, errorInfo string) string
	Next(register string, v interface{}, isString bool, errorInfo string) string
	Panic(v1 interface{}, errorInfo string, usesGr bool) string
	TypeStart(*types.Named, string) string
	//TypeEnd(*types.Named, string) string
	TypeAssert(Register string, X ssa.Value, AssertedType types.Type, CommaOk bool, errorInfo string) string
	EmitTypeInfo() string
	EmitInvoke(register, path string, isGo, isDefer, usesGr bool, callCommon interface{}, errorInfo string) string
	FunctionOverloaded(pkg, fun string) bool
	Select(isSelect bool, register string, v interface{}, CommaOK bool, errorInfo string) string
	PeepholeOpt(opt, register string, code []ssa.Instruction, errorInfo string) string
	DebugRef(userName string, v interface{}, errorInfo string) string
	CanInline(v interface{}) bool
	PhiCode(allTargets bool, targetPhi int, code []ssa.Instruction, errorInfo string) string
	InitLang(*Compilation, *LanguageEntry) Language
}

// LanguageEntry holds the static infomation about each of the languages, expect this list to extend as more languages are added.
type LanguageEntry struct {
	Language                           // A type implementing all of the interface methods.
	buffer                bytes.Buffer // Where the output is collected.
	InstructionLimit      int          // How many instructions in a function before we need to split it up.
	SubFnInstructionLimit int          // When we split up a function, how large can each sub-function be?
	PackageConstVarName   string       // The special constant name to specify a Package/Module name in the target language.
	HeaderConstVarName    string       // The special constant name for a target-specific header.
	Goruntime             string       // The location of the core implementation go runtime code for this target language.
	TestFS                string       // the location of the test zipped file system, if present
	LineCommentMark       string       // what marks the comment at the end of a line
	StatementTerminator   string       // what marks the end of a statement, usually ";"
	PseudoPkgPaths        []string     // paths of packages containing pseudo-functions
	IgnorePrefixes        []string     // the prefixes to code to ignore during peephole optimization
	files                 []FileOutput // files to write if no errors in compilation
	GOROOT                string       // static part of the GOROOT path
	TgtDir                string       // Target directory to write to
}

// FileOutput provides temporary storage of output file data, pending correct compilation
type FileOutput struct {
	filename string
	data     []byte
}

// LanguageList holds the languages that can be targeted, and compilation run data
var LanguageList = make([]LanguageEntry, 0, 10)
var languageListAppendMutex sync.Mutex

// FindTargetLang returns the 1st LanguageList entry for the given language
func FindTargetLang(s string) (k int, e error) {
	var v LanguageEntry
	for k, v = range LanguageList {
		if v.LanguageName() == s {
			return
		}
	}
	return -1, errors.New("Target Language Not Found: " + s)
}

// Utility comment emitter function.
func (comp *Compilation) emitComment(cmt string) {
	l := comp.TargetLang
	fmt.Fprintln(&LanguageList[l].buffer, LanguageList[l].Comment(cmt))
}

// is there more than one package with this name?
// TODO consider using this function in pogo.emitFunctions()
func (comp *Compilation) isDupPkg(pn string) bool {
	pnCount := 0
	ap := comp.rootProgram.AllPackages()
	for p := range ap {
		if pn == ap[p].Pkg.Name() {
			pnCount++
		}
	}
	return pnCount > 1
}

// FuncPathName returns a unique function path and name.
func (comp *Compilation) FuncPathName(fn *ssa.Function) (path, name string) {
	rx := fn.Signature.Recv()
	pf := tgoutil.MakeID(comp.rootProgram.Fset.Position(fn.Pos()).String()) //fmt.Sprintf("fn%d", fn.Pos())
	if rx != nil {                                                          // it is not the name of a normal function, but that of a method, so append the method description
		pf = rx.Type().String() // NOTE no underlying()
	} else {
		if fn.Pkg != nil {
			pf = fn.Pkg.Pkg.Path() // was .Name(), but not unique
		} else {
			goroot := tgoutil.MakeID(LanguageList[comp.TargetLang].GOROOT + string(os.PathSeparator))
			pf1 := strings.Split(pf, goroot) // make auto-generated names shorter
			if len(pf1) == 2 {
				pf = pf1[1]
			} // TODO use GOPATH for names not in std pkgs
		}
	}
	return pf, fn.Name()
}
