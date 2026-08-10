package kube

// Kute's own CronJob/Job association and suspension annotations (v0.8.0
// plan §4.2). These exist because Kubernetes gives Kute no other durable way
// to answer two questions truthfully:
//
//  1. "Which CronJob does this standalone Job belong to?" — a manually
//     triggered Job (§36b) is deliberately left ownerless (no
//     OwnerReferences) so CronJob history garbage collection can never
//     delete it; internal/resources/cronjobs.go's association logic reads
//     these back instead of inferring anything from the Job's name.
//  2. "When did Kute suspend this CronJob, and is that timestamp still
//     trustworthy?" — spec.suspend carries no timestamp of its own, so a
//     suspend/resume cycle records one here, keyed to the generation it was
//     stamped at (§3.3) so a later external suspend can't reuse a stale
//     value.
//
// internal/kube/mutate.go (v0.8.0 Phase 2) is what actually stamps these on
// a live/fake cluster; internal/resources/cronjobs.go (Phase 1) only reads
// them, which is why the constants live here rather than in resources —
// kube has no dependency on resources, so resources importing kube for
// these (as it already does for ResourceKind) creates no cycle.
const (
	// AnnotationCronJobName is a manual Job's source CronJob name — the
	// name-fallback half of the association match used only when no UID is
	// available on either side (sparse fake objects). Never sufficient
	// alone against a real cluster: see AnnotationCronJobUID.
	AnnotationCronJobName = "kute.dev/cronjob-name"
	// AnnotationCronJobUID is a manual Job's source CronJob UID — the
	// authoritative half of the association match whenever it's present.
	// A Job whose annotation UID doesn't match the CronJob's current UID is
	// never associated, even if the name still does (delete/recreate must
	// not inherit stale history).
	AnnotationCronJobUID = "kute.dev/cronjob-uid"
	// AnnotationTriggeredBy names who/what asked for a manual run (§36b) —
	// JobSummary.Creator's source for a JobSourceManual run.
	AnnotationTriggeredBy = "kute.dev/triggered-by"
	// AnnotationTriggeredAt is the RFC3339 instant a manual run was staged
	// (§36b) — kept distinct from the Job's own creationTimestamp so a
	// staged-then-delayed create still records the moment the operator
	// asked, not the moment the API server accepted it.
	AnnotationTriggeredAt = "kute.dev/triggered-at"
	// AnnotationSuspendedAt is the RFC3339 instant Kute itself suspended a
	// CronJob (§3.3) — trusted only alongside AnnotationSuspendedGeneration
	// matching the CronJob's current generation; see
	// internal/resources/cronjobs.go's suspendedAt.
	AnnotationSuspendedAt = "kute.dev/suspended-at"
	// AnnotationSuspendedGeneration is the CronJob generation
	// AnnotationSuspendedAt was stamped against. A generation mismatch
	// means the object changed since Kute's suspend (or an external
	// resume/resuspend happened), so the timestamp can no longer be
	// trusted and must render as "start unknown" rather than a guess.
	AnnotationSuspendedGeneration = "kute.dev/suspended-generation"
)
