package domain

import "testing"

// Every character an account may hold in its name has to survive being stamped
// on a comment.
//
// TestEveryStorableDisplayNameCanAuthorAComment settled the lengths after they
// drifted apart. The character rules had drifted the same way and further: this
// normalizer exempted newline and tab and stopped at DEL, while the comment one
// refuses anything unicode.IsControl calls a control. A name in the gap saved
// fine and then failed every comment the account tried to write, under an error
// naming a field its owner never typed.
func TestNoStorableDisplayNameCarriesACharacterAcommentRefuses(t *testing.T) {
	for name, candidate := range map[string]string{
		"an interior newline":      "Che\nems",
		"an interior tab":          "Che\tems",
		"a carriage return":        "Che\rems",
		"a C1 control":             "Che\u0085ems",
		"the delete character":     "Che\u007fems",
		"a zero byte":              "Che\x00ems",
		"only a control character": "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeDisplayName(candidate); !HasCode(err, CodeInvalidArgument) {
				t.Fatalf("display name error = %v", err)
			}
			if _, err := NormalizeCommentAuthorName(candidate); err == nil {
				t.Fatal("a comment author name with a control character was accepted")
			}
		})
	}

	// The rule bites on characters and nothing else. An ordinary name still
	// passes both, surrounding whitespace and all.
	display, err := NormalizeDisplayName("  Cheems  ")
	if err != nil || display != "Cheems" {
		t.Fatalf("display name = %q, err = %v", display, err)
	}
	if _, err := NormalizeCommentAuthorName(display); err != nil {
		t.Fatalf("a storable display name cannot author a comment: %v", err)
	}
}

// A bio is not a name, and the two normalizers stop agreeing here on purpose.
//
// It is typed into a textarea and read as a paragraph, so the line breaks
// somebody put in it are theirs to keep. Nothing flattens a bio onto a comment
// the way a display name is flattened.
func TestBioKeepsTheLineBreaksSomebodyTyped(t *testing.T) {
	bio := "沉浸在万花筒的幻境中吧。\n第二行。\t带缩进"
	kept, err := NormalizeBio(bio)
	if err != nil || kept != bio {
		t.Fatalf("bio = %q, err = %v", kept, err)
	}
	for name, candidate := range map[string]string{
		"a carriage return": "第一行\r第二行",
		"a C1 control":      "第一行\u0085第二行",
		"a zero byte":       "第一行\x00第二行",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeBio(candidate); !HasCode(err, CodeInvalidArgument) {
				t.Fatalf("bio error = %v", err)
			}
		})
	}
}
