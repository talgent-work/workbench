package mysql

import (
	"fmt"

	"go.uber.org/fx"
	"xorm.io/xorm"
)

// Module owns one named database connection and its shutdown hook. The
// matching Config must be supplied under the same name by process composition.
// The connection opens where the process lists the module, so it closes after
// the business modules listed later (fx stops hooks in reverse order).
func Module(name string) fx.Option {
	tag := fmt.Sprintf(`name:%q`, name)
	return fx.Module("mysql."+name,
		fx.Provide(fx.Annotate(NewEngine,
			fx.ParamTags("", tag, ""),
			fx.ResultTags(tag),
		)),
		fx.Invoke(fx.Annotate(func(*xorm.Engine) {}, fx.ParamTags(tag))),
	)
}

// Bind makes only the selected database available to a module's repositories.
// The local alias neither opens another connection nor owns its lifecycle.
func Bind(name string) fx.Option {
	return fx.Provide(fx.Annotate(
		func(engine *xorm.Engine) *xorm.Engine { return engine },
		fx.ParamTags(fmt.Sprintf(`name:%q`, name)),
	), fx.Private)
}
