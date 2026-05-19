package analyzer

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"time"

	"golang.org/x/tools/go/packages"
)

const defaultEmbeddingVersion = 1

func ExtractCodeSymbolsFromSyntax(pkgs []*packages.Package) ([]CodeSymbol, []SymbolHash) {
	symbols := make([]CodeSymbol, 0, 128)
	hashes := make([]SymbolHash, 0, 128)
	now := time.Now().UTC()
	for _, pkg := range pkgs {
		if pkg == nil || pkg.Fset == nil || pkg.PkgPath == "" {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Name == nil {
						continue
					}
					symbol, ok := codeSymbolForNode(pkg, d, "function", symbolNameForFunc(pkg.PkgPath, d))
					if !ok {
						continue
					}
					symbols = append(symbols, symbol)
					hashes = append(hashes, symbolHashForCodeSymbol(symbol, now))
				case *ast.GenDecl:
					if d.Tok != token.TYPE {
						continue
					}
					for _, spec := range d.Specs {
						typeSpec, ok := spec.(*ast.TypeSpec)
						if !ok || typeSpec.Name == nil {
							continue
						}
						symbolType := typeSymbolKind(typeSpec.Type)
						symbol, ok := codeSymbolForNode(pkg, typeSpec, symbolType, pkg.PkgPath+"."+typeSpec.Name.Name)
						if !ok {
							continue
						}
						symbols = append(symbols, symbol)
						hashes = append(hashes, symbolHashForCodeSymbol(symbol, now))
					}
				}
			}
		}
	}
	return symbols, hashes
}

func symbolNameForFunc(pkgPath string, fd *ast.FuncDecl) string {
	name := fd.Name.Name
	if recv := receiverName(fd); recv != "" {
		return pkgPath + "." + recv + "." + name
	}
	return pkgPath + "." + name
}

func typeSymbolKind(expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return "type"
	}
}

func codeSymbolForNode(pkg *packages.Package, node ast.Node, symbolType, id string) (CodeSymbol, bool) {
	start := pkg.Fset.Position(node.Pos())
	end := pkg.Fset.Position(node.End())
	if start.Filename == "" || start.Line == 0 {
		return CodeSymbol{}, false
	}
	source := formattedNode(pkg.Fset, node)
	if source == "" {
		return CodeSymbol{}, false
	}
	return CodeSymbol{
		ID:          id,
		Type:        symbolType,
		PackagePath: pkg.PkgPath,
		Name:        shortSymbolName(id),
		FilePath:    start.Filename,
		LineStart:   start.Line,
		LineEnd:     end.Line,
		Source:      source,
	}, true
}

func formattedNode(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return fmt.Sprint(node)
	}
	return buf.String()
}

func symbolHashForCodeSymbol(symbol CodeSymbol, createdAt time.Time) SymbolHash {
	return SymbolHash{
		SymbolID:         symbol.ID,
		FilePath:         symbol.FilePath,
		SymbolType:       symbol.Type,
		SymbolName:       symbol.Name,
		SourceHash:       SymbolSourceHash(symbol.Source),
		EmbeddingVersion: defaultEmbeddingVersion,
		CreatedAt:        createdAt,
	}
}

func SymbolSourceHash(source string) string {
	sum := md5.Sum([]byte(source))
	return hex.EncodeToString(sum[:])
}

func shortSymbolName(id string) string {
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '.' || id[i] == '/' {
			return id[i+1:]
		}
	}
	return id
}
