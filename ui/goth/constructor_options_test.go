package goth

import "testing"

func TestOptionsValidateFinalSettingsAndRemainReusable(t *testing.T) {
	opts := []Option{WithProfile(Profile(99)), WithProfile(Full), WithAssetBasePath("https://invalid.example"), WithAssetBasePath("/host/assets/"), WithThemeStylesheetPath("//invalid.example"), WithThemeStylesheetPath("/host/theme.css")}
	for range 2 {
		bundle, err := New(opts...)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Profile() != Full || bundle.AssetBasePath() != "/host/assets" || bundle.themeStylesheetPath != "/host/theme.css" {
			t.Fatalf("resolved bundle = %+v", bundle)
		}
	}
	bundle, err := New(nil)
	if bundle != nil || err == nil || err.Error() != "goth: nil Option" {
		t.Fatalf("New(nil) = %v, %v", bundle, err)
	}
}
