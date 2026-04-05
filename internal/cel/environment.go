package cel

import (
	"reflect"

	celgo "github.com/google/cel-go/cel"
	celtypes "github.com/google/cel-go/common/types"
	celref "github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
)

// NewOptiPilotEnv creates a CEL environment with all OptiPilot types and custom
// functions registered. The environment is safe to share across goroutines.
func NewOptiPilotEnv() (*celgo.Env, error) {
	return celgo.NewEnv(
		// Register native Go types with cel tag support for snake_case field names.
		ext.NativeTypes(
			ext.ParseStructTags(true),
			reflect.TypeOf(CandidatePlan{}),
			reflect.TypeOf(CurrentState{}),
			reflect.TypeOf(SLOStatus{}),
			reflect.TypeOf(TenantStatus{}),
			reflect.TypeOf(ForecastResult{}),
			reflect.TypeOf(ClusterState{}),
		),

		// Declare top-level variables available to every expression.
		celgo.Variable("candidate", celgo.ObjectType("cel.CandidatePlan")),
		celgo.Variable("current", celgo.ObjectType("cel.CurrentState")),
		celgo.Variable("slo", celgo.ObjectType("cel.SLOStatus")),
		celgo.Variable("tenant", celgo.ObjectType("cel.TenantStatus")),
		celgo.Variable("forecast", celgo.ObjectType("cel.ForecastResult")),
		celgo.Variable("metrics", celgo.MapType(celgo.StringType, celgo.DoubleType)),
		celgo.Variable("cluster", celgo.ObjectType("cel.ClusterState")),

		// spotRisk(instanceType string, az string) double
		celgo.Function("spotRisk",
			celgo.Overload(
				"spotRisk_string_string",
				[]*celgo.Type{celgo.StringType, celgo.StringType},
				celgo.DoubleType,
				celgo.BinaryBinding(func(lhs, rhs celref.Val) celref.Val {
					instanceType, ok1 := lhs.Value().(string)
					az, ok2 := rhs.Value().(string)
					if !ok1 || !ok2 {
						return celtypes.NewErr("spotRisk: arguments must be strings")
					}
					return celtypes.Double(SpotRiskFunc(instanceType, az))
				}),
			),
		),

		// carbonIntensity(region string) double
		celgo.Function("carbonIntensity",
			celgo.Overload(
				"carbonIntensity_string",
				[]*celgo.Type{celgo.StringType},
				celgo.DoubleType,
				celgo.UnaryBinding(func(v celref.Val) celref.Val {
					region, ok := v.Value().(string)
					if !ok {
						return celtypes.NewErr("carbonIntensity: argument must be a string")
					}
					return celtypes.Double(CarbonIntensityFunc(region))
				}),
			),
		),

		// costRate(instanceType string, region string, spot bool) double
		celgo.Function("costRate",
			celgo.Overload(
				"costRate_string_string_bool",
				[]*celgo.Type{celgo.StringType, celgo.StringType, celgo.BoolType},
				celgo.DoubleType,
				celgo.FunctionBinding(func(args ...celref.Val) celref.Val {
					if len(args) != 3 {
						return celtypes.NewErr("costRate: expected 3 arguments")
					}
					instanceType, ok1 := args[0].Value().(string)
					region, ok2 := args[1].Value().(string)
					spot, ok3 := args[2].Value().(bool)
					if !ok1 || !ok2 || !ok3 {
						return celtypes.NewErr("costRate: invalid argument types")
					}
					return celtypes.Double(CostRateFunc(instanceType, region, spot))
				}),
			),
		),
	)
}
