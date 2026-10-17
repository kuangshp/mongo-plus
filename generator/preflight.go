package generator

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kuangshp/mongo-plus/schema"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// PlanSchema 预览 Prepare 中已冻结的 JSON 定义，不重新读取源文件。
func (p *Plan) PlanSchema(ctx context.Context, db *mongo.Database) ([]schema.Change, error) {
	if p == nil || len(p.entities) == 0 {
		return nil, fmt.Errorf("生成计划尚未由 Prepare 准备")
	}
	return schema.Plan(ctx, db, p.entities)
}

// ApplySchema 同步同一快照；调用方负责连接生命周期、超时和迁移发布策略。
func (p *Plan) ApplySchema(ctx context.Context, db *mongo.Database) ([]schema.Change, error) {
	if p == nil || len(p.entities) == 0 {
		return nil, fmt.Errorf("生成计划尚未由 Prepare 准备")
	}
	return schema.Apply(ctx, db, p.entities)
}

func (p *Plan) validateOutputs() error {
	// overlay 表示生成后的有效文件。已有业务扩展/DTO/VO 按保留策略参与检查，
	// 即将覆盖的生成文件使用新内容，避免旧版本声明影响合法的重新生成。
	overlay := make(map[string][]byte, len(p.outputs))
	directories := map[string]bool{}
	for _, out := range p.outputs {
		data := out.data
		old, err := os.ReadFile(out.path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			if out.preserve {
				data = old
			} else if !bytes.HasPrefix(old, []byte(generated)) {
				return fmt.Errorf("拒绝覆盖非生成文件 %s", out.path)
			}
		}
		overlay[out.path] = data
		directories[filepath.Dir(out.path)] = true
	}
	for dir := range directories {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			return err
		}
		for _, path := range paths {
			if _, ok := overlay[path]; ok {
				continue
			}
			// 测试有独立包和编译约束，不属于本次生成的生产包声明空间。
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			match, err := build.Default.MatchFile(dir, filepath.Base(path))
			if err != nil {
				return err
			}
			if !match {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			overlay[path] = data
		}
	}
	paths := make([]string, 0, len(overlay))
	for path := range overlay {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	names := map[string]string{}
	for _, path := range paths {
		file, err := parser.ParseFile(token.NewFileSet(), path, overlay[path], parser.AllErrors)
		if err != nil {
			return fmt.Errorf("生成预检: %w", err)
		}
		if file.Name.Name != filepath.Base(filepath.Dir(path)) {
			return fmt.Errorf("生成包名称冲突: %s 的包名为 %s，目录要求 %s", path, file.Name.Name, filepath.Base(filepath.Dir(path)))
		}
		for _, decl := range file.Decls {
			var declared []string
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						declared = append(declared, s.Name.Name)
					case *ast.ValueSpec:
						for _, name := range s.Names {
							declared = append(declared, name.Name)
						}
					}
				}
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name != "init" {
					declared = append(declared, d.Name.Name)
				}
			}
			for _, name := range declared {
				if name == "_" {
					continue
				}
				key := filepath.Dir(path) + "\x00" + file.Name.Name + "\x00" + name
				if previous, ok := names[key]; ok {
					return fmt.Errorf("生成声明名称冲突 %s: %s 与 %s", name, previous, path)
				}
				names[key] = path
			}
		}
	}
	return nil
}
