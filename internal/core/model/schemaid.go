package model

// SchemaID is a versioned schema identifier of the form ddf.<name>/v1
// (spec §4.1).
type SchemaID string

// Schema IDs of the records and the role results.
const (
	SchemaIDTask           SchemaID = "ddf.task/v1"
	SchemaIDRun            SchemaID = "ddf.run/v1"
	SchemaIDChange         SchemaID = "ddf.change/v1"
	SchemaIDReview         SchemaID = "ddf.review/v1"
	SchemaIDFinding        SchemaID = "ddf.finding/v1"
	SchemaIDQuestion       SchemaID = "ddf.question/v1"
	SchemaIDDecision       SchemaID = "ddf.decision/v1"
	SchemaIDIntent         SchemaID = "ddf.intent/v1"
	SchemaIDEvent          SchemaID = "ddf.event/v1"
	SchemaIDPlannerResult  SchemaID = "ddf.planner.result/v1"
	SchemaIDExecutorResult SchemaID = "ddf.executor.result/v1"
	SchemaIDReviewerResult SchemaID = "ddf.reviewer.result/v1"
)
