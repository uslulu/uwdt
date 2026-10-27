package backend

import (
	"reflect"
	"testing"

	xtun "github.com/xtls/xray-core/proxy/tun"
)

// closeTunInbound лезет в неэкспортируемые поля xray-core: при обновлении
// xray-core этот тест покажет, что поле переименовали
func TestXrayTunHandlerHasStackField(t *testing.T) {
	if _, ok := reflect.TypeOf(xtun.Handler{}).FieldByName("stack"); !ok {
		t.Fatal("у tun.Handler нет поля stack — закрытие TUN Xray сломается")
	}
}

type fakeCloser struct {
	closed *[]string
	name   string
}

func (f fakeCloser) Close() error { *f.closed = append(*f.closed, f.name); return nil }

type fakeStack struct {
	fakeCloser
	tun any
}

type fakeHandler struct{ stack any }

func TestCloseTunInbound(t *testing.T) {
	var closed []string
	h := &fakeHandler{stack: &fakeStack{fakeCloser: fakeCloser{&closed, "стек"}, tun: fakeCloser{&closed, "интерфейс"}}}
	if err := closeTunInbound(h); err != nil {
		t.Fatal(err)
	}
	if len(closed) != 2 || closed[0] != "стек" || closed[1] != "интерфейс" {
		t.Errorf("закрыто %v, want [стек интерфейс]", closed)
	}
	if err := closeTunInbound(&struct{ other int }{}); err == nil {
		t.Error("без поля stack должна быть ошибка")
	}
}
