package domain

// OptionalModule is a module an institution may turn off without touching code (RF01). The core modules (structure,
// enrollment, attendance, grades…) are always on; these are the ones served by a gRPC service no other module uses,
// so the API can refuse that service whole. Live classes, virtual tutoring and permanence are not here: their
// services also serve tutoring, psychology and the classroom
type OptionalModule struct {
	Key  string
	Name string
}

// OptionalModules in the order Configuración lists them; the keys are the panel's module keys
var OptionalModules = []OptionalModule{
	{Key: "payments", Name: "Pensiones y pagos"},
	{Key: "classroom", Name: "Aula virtual"},
	{Key: "mock-exams", Name: "Simulacros, ranking y banco de preguntas"},
	{Key: "compliance", Name: "Cumplimiento DRE"},
	{Key: "apafa", Name: "APAFA"},
}

// IsOptionalModule tells whether key names one of OptionalModules
func IsOptionalModule(key string) bool {
	for _, m := range OptionalModules {
		if m.Key == key {
			return true
		}
	}
	return false
}

// ModuleState is an optional module and whether the institution has it on
type ModuleState struct {
	OptionalModule
	Enabled bool
}

var (
	ErrModuleDisabled  = newError(KindForbidden, "module_disabled", "Este módulo está desactivado en tu institución.")
	ErrModuleUnknown   = newError(KindInvalid, "module_unknown", "Ese módulo no se puede activar ni desactivar.")
	ErrModulesReadOnly = newError(KindForbidden, "modules_read_only", "Solo dirección activa o desactiva los módulos.")
)
