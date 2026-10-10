package ui

// Pins #215 (#199 WI-11; UI-11): the seam list is typed once. The seams
// are listed in engineRunSeams, again as engineRun's own fields, and
// again in withSeams; prepareRepair sits in the bundle but is copied by
// hand in app.go, and prepareRotate is missing from it. Adding a seam
// meant three edits. The bundle is embedded in engineRun, holds
// prepareRepair and prepareRotate, and app.go hands it down whole.
//
// The structure is checked by reflection (it compiles whether or not
// the change has landed); the wiring by a scan of app.go and by driving
// a rotation through an AppModel.

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

var seamsType = reflect.TypeOf(engineRunSeams{})

func TestEngineRunSeamsHoldsRepairAndRotate(t *testing.T) {
	for _, name := range []string{"prepareRepair", "prepareRotate"} {
		f, ok := seamsType.FieldByName(name)
		if !ok {
			t.Errorf("engineRunSeams has no %s field", name)
			continue
		}
		if want := name + "Func"; f.Type.Name() != want {
			t.Errorf("engineRunSeams.%s has type %s, want %s", name, f.Type, want)
		}
	}
}

// engineRun embeds the bundle instead of retyping every field.
func TestEngineRunEmbedsTheSeamBundle(t *testing.T) {
	runType := reflect.TypeOf(engineRun{})
	embedded := false
	for i := 0; i < runType.NumField(); i++ {
		if f := runType.Field(i); f.Anonymous && f.Type == seamsType {
			embedded = true
		}
	}
	if !embedded {
		t.Fatal("engineRun does not embed engineRunSeams")
	}
	// An embedded bundle plus a retyped field of the same name would be
	// the copy again, shadowing the bundle's.
	for i := 0; i < seamsType.NumField(); i++ {
		name := seamsType.Field(i).Name
		for j := 0; j < runType.NumField(); j++ {
			if f := runType.Field(j); !f.Anonymous && f.Name == name {
				t.Errorf("engineRun retypes the seam %s next to the embedded bundle", name)
			}
		}
	}
}

// Passed already: the flows that take their seams from the bundle keep
// holding it whole.
func TestInstallAndUpdateKeepTheBundle(t *testing.T) {
	for _, v := range []any{InstallModel{}, UpdateModel{}, AppModel{}} {
		typ := reflect.TypeOf(v)
		held := false
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).Type == seamsType {
				held = true
			}
		}
		if !held {
			t.Errorf("%s holds no engineRunSeams", typ)
		}
	}
}

// TestAppWiresTheBundleOnly: app.go names neither prepareRepair nor
// prepareRotate; it passes the bundle. Today it copies prepareRepair by
// hand.
func TestAppWiresTheBundleOnly(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "app.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	bundleUsed := false
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		switch id.Name {
		case "prepareRepair", "prepareRotate":
			t.Errorf("app.go:%d names the seam %s; it should hand the bundle down", fset.Position(id.Pos()).Line, id.Name)
		case "flowSeams":
			bundleUsed = true
		}
		return true
	})
	if !bundleUsed {
		t.Error("app.go never reads flowSeams: the scan above would pass vacuously")
	}
}

// rotationPlanScript is the plan the repair engine gives when a database
// credential needs rotating: the one executable action.
const rotationPlanScript = `
echo 'plan action=rotate-database-credential resolves=database-credential-mismatch mutation=credential-rotation backup=required'
echo 'plan result=ready actions=1 manual=0'
exit 3`

// TestAppHandsRotateSeamToRepair drives Repair through an AppModel whose
// bundle carries a prepareRotate seam. Choosing the rotation must call
// that seam with the app's target directory: it is how app.go wires the
// bundle rather than field by field (today the field does not exist,
// and RepairModel is given its rotate seam by tests directly).
func TestAppHandsRotateSeamToRepair(t *testing.T) {
	var (
		mu     sync.Mutex
		called []string
	)
	rotate := func(_ context.Context, dir string) (*engine.Stream, io.WriteCloser, error) {
		mu.Lock()
		defer mu.Unlock()
		called = append(called, dir)
		return nil, nil, errors.New("rotate seam reached")
	}

	seams := engineRunSeams{prepareRepair: fakeRepairStream(rotationPlanScript)}
	field := reflect.ValueOf(&seams).Elem().FieldByName("prepareRotate")
	if !field.IsValid() {
		t.Fatal("engineRunSeams has no prepareRotate field")
	}
	fn := reflect.ValueOf(rotate)
	if !fn.Type().ConvertibleTo(field.Type()) {
		t.Fatalf("prepareRotate has type %s; want func(context.Context, string) (*engine.Stream, io.WriteCloser, error)", field.Type())
	}
	// The field is unexported; reflect needs its address to set it.
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(fn.Convert(field.Type()))

	queue := newSink()
	m := NewAppModelNoAnimation().WithSender(queue.Send).WithoutVolumeCheck()
	m.targetDir = t.TempDir()
	m.flowSeams = seams
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 30}, key(tea.KeyDown), key(tea.KeyDown)) // Install, Update, Repair
	m, cmd := appUpdate(t, m, key(tea.KeyEnter))
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	app := &m
	loop := newFlowLoop(t, queue, func(msg tea.Msg) tea.Cmd {
		var next tea.Cmd
		*app, next = appUpdate(t, *app, msg)
		return next
	})
	loop.settle(cmd)

	// The plan screen proves the repair seam reached the flow and the
	// rotation entry is there to be chosen: the absence checked below
	// cannot be vacuous.
	if s := ansi.Strip(app.View().Content); !strings.Contains(s, "Rotate database credentials") {
		t.Fatalf("the plan screen never offered the rotation:\n%s", s)
	}
	mu.Lock()
	before := len(called)
	mu.Unlock()
	if before != 0 {
		t.Fatalf("the rotate seam ran before the rotation was chosen (%d calls)", before)
	}

	loop.settle(loop.update(key(tea.KeyEnter))) // the rotation is preselected

	mu.Lock()
	defer mu.Unlock()
	if len(called) != 1 {
		t.Fatalf("the bundle's prepareRotate ran %d times, want 1 (the app did not hand it to Repair)", len(called))
	}
	if called[0] != m.targetDir {
		t.Errorf("prepareRotate got dir %q, want the app's target directory %q", called[0], m.targetDir)
	}
}
