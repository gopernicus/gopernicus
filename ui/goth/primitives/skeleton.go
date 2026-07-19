package primitives

import "github.com/a-h/templ"

// SkeletonProps configures Skeleton (P22, family F1): a non-announcing decorative
// placeholder box. It is aria-hidden so assistive technology never reads it, and
// its pulse animation collapses under prefers-reduced-motion. The caller sizes it
// via Base.Class. The zero value is a valid default placeholder.
type SkeletonProps struct {
	Base
}

func skeletonClass(p SkeletonProps) string { return classNames("goth-skeleton", p.Class) }

func skeletonAttrs(p SkeletonProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{
		"data-slot":   "skeleton",
		"aria-hidden": "true",
	})
}
