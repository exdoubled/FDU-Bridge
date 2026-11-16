package appconf

import "testing"

func TestDecodeMigratesLegacyProxyOnlyToGlobal(t *testing.T) {
	c, err := Decode([]byte(`{"integration":{"mode":"proxy"},"campusConsent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.StandaloneMode != "global" || c.CampusConsent {
		t.Fatalf("legacy proxy-only config not migrated: mode=%q consent=%v", c.StandaloneMode, c.CampusConsent)
	}
}

func TestDecodeKeepsExplicitStandaloneChoices(t *testing.T) {
	c, err := Decode([]byte(`{"integration":{"mode":"proxy"},"standaloneMode":"rule","campusConsent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.StandaloneMode != "rule" || !c.CampusConsent {
		t.Fatalf("explicit standalone config changed: mode=%q consent=%v", c.StandaloneMode, c.CampusConsent)
	}
	c, err = Decode([]byte(`{"integration":{"mode":"auto"},"campusConsent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.StandaloneMode != "" || !c.CampusConsent {
		t.Fatalf("Clash config changed: mode=%q consent=%v", c.StandaloneMode, c.CampusConsent)
	}
}
