package primitives

import "github.com/a-h/templ"

// AvatarProps configures the Avatar container (P03, family F3). The caller
// composes AvatarImage and AvatarFallback as children. The zero value is a valid
// default-size avatar.
type AvatarProps struct {
	Base
}

// AvatarImageProps configures AvatarImage. Src is a validated URL and Alt is the
// required accessible name for the image; an empty Alt renders alt="" (a
// decorative image) which is only correct when a labelled fallback carries the
// name.
type AvatarImageProps struct {
	Base
	Src URL
	Alt string
}

// AvatarFallbackProps configures AvatarFallback, shown when no image loads. Its
// content (initials or an icon) is passed as templ children. In the no-JS
// baseline the fallback sits behind the image and shows through when the image
// is absent or fails to load.
type AvatarFallbackProps struct {
	Base
}

func avatarClass(p AvatarProps) string { return classNames("goth-avatar", p.Class) }

func avatarAttrs(p AvatarProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "avatar"})
}

func avatarImageClass(p AvatarImageProps) string {
	return classNames("goth-avatar-image", p.Class)
}

func avatarImageAttrs(p AvatarImageProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "avatar-image"})
}

func avatarFallbackClass(p AvatarFallbackProps) string {
	return classNames("goth-avatar-fallback", p.Class)
}

func avatarFallbackAttrs(p AvatarFallbackProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "avatar-fallback"})
}
