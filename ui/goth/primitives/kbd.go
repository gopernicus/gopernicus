package primitives

import "github.com/a-h/templ"

// Kbd (P15, family F3) is a compound: Kbd renders a single keyboard key
// (semantic <kbd>) and KbdGroup groups several Kbd into one shortcut. The zero
// value of each Props is valid. data-slot hooks: kbd, kbd-group.
type KbdProps struct{ Base }

// KbdGroupProps configures KbdGroup.
type KbdGroupProps struct{ Base }

func kbdClass(p KbdProps) string { return classNames("goth-kbd", p.Class) }

func kbdAttrs(p KbdProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "kbd"})
}

func kbdGroupClass(p KbdGroupProps) string { return classNames("goth-kbd-group", p.Class) }

func kbdGroupAttrs(p KbdGroupProps) templ.Attributes {
	return ownedAttrs(p.Base, templ.Attributes{"data-slot": "kbd-group"})
}
