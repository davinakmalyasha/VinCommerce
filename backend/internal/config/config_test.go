package config

import (
	"strings"
	"testing"
)

// composeFallbackSecret is the value that used to ship in
// infra/compose/compose.full.yaml. It is 41 characters — long enough to pass
// a length check — and is not the single sentinel string the old validator
// also compared against, so it sailed past the production guard entirely.
const composeFallbackSecret = "local-dev-secret-please-change-me-32chars"

func prodConfig(secret string) *Config {
	return &Config{
		Environment: string(EnvProduction),
		Auth:        AuthConfig{JWTSecret: secret},
		Payments:    PaymentConfig{Gateway: "midtrans", MidtransEnv: "production", PayoutLagDays: 7},
		Database:    DatabaseConfig{SSLMode: "require", Password: "s3cr3t-from-vault"},
	}
}

func TestValidateRejectsPublishedComposeSecret(t *testing.T) {
	// The exact regression: APP_ENV=production + the committed compose
	// default used to boot a production API signed with a public key.
	err := prodConfig(composeFallbackSecret).Validate()
	if err == nil {
		t.Fatal("Validate accepted the JWT secret published in compose.full.yaml")
	}
	if !strings.Contains(err.Error(), "published in this repository") {
		t.Fatalf("error = %q, want an explicit published-secret message", err)
	}
}

func TestValidateRejectsAllKnownPublicSecrets(t *testing.T) {
	for _, secret := range knownPublicSecrets {
		if !IsKnownPublicSecret(secret) {
			t.Errorf("IsKnownPublicSecret(%q) = false, want true", secret)
		}
		if err := prodConfig(secret).Validate(); err == nil {
			t.Errorf("Validate accepted known-public secret %q", secret)
		}
	}
}

func TestValidateRejectsLowEntropySecrets(t *testing.T) {
	cases := map[string]string{
		"too short":        "short",
		"exactly 31":       strings.Repeat("a", 31),
		"single character": strings.Repeat("x", 64),
		"two characters":   strings.Repeat("ab", 32),
		"empty":            "",
	}
	for name, secret := range cases {
		if err := prodConfig(secret).Validate(); err == nil {
			t.Errorf("%s: Validate accepted %q", name, secret)
		}
	}
}

func TestValidateAcceptsStrongSecret(t *testing.T) {
	// A real 48-byte base64 secret has high distinct-rune entropy.
	strong := "kR3!xQm2#Lp9vZt7@Nw4&cYb6^fDh8*jS1aEg5+Ui3oPl9=kZ2xCv7bNm4Qw"
	if err := prodConfig(strong).Validate(); err != nil {
		t.Fatalf("Validate rejected a strong secret: %v", err)
	}
}

func TestDevelopmentToleratesWeakSecret(t *testing.T) {
	// Local development must keep working with the documented default.
	c := prodConfig(composeFallbackSecret)
	c.Environment = string(EnvDevelopment)
	c.Payments = PaymentConfig{Gateway: "sandbox", MidtransEnv: "sandbox", PayoutLagDays: 7}
	c.Database = DatabaseConfig{SSLMode: "disable", Password: "vincom_dev"}
	if err := c.Validate(); err != nil {
		t.Fatalf("development Validate rejected dev defaults: %v", err)
	}
}

// A payout lag outside the supported range must stop the boot, in development too.
//
// The alternative -- treating an unset or out-of-range value as "use the default" --
// is a zero that silently means something, and a payout term that quietly is not
// the configured one is worse than a refusal: the operator believes they have a
// longer lag than they have.
func TestAnOutOfRangePayoutLagStopsTheBoot(t *testing.T) {
	for _, days := range []int{0, -1, 91, 365} {
		c := prodConfig("a-strong-enough-secret-for-this-test-only")
		c.Payments.PayoutLagDays = days
		if err := c.Validate(); err == nil {
			t.Errorf("PAYOUT_LAG_DAYS=%d was accepted; a payout term outside the "+
				"supported range must be refused rather than falling back", days)
		}
	}
	// And the edges are valid, so the check is a range and not a rejection of
	// everything unusual.
	for _, days := range []int{1, 7, 90} {
		c := prodConfig("a-strong-enough-secret-for-this-test-only")
		c.Payments.PayoutLagDays = days
		if err := c.Validate(); err != nil {
			t.Errorf("PAYOUT_LAG_DAYS=%d was refused: %v", days, err)
		}
	}
}

func TestParseEnvironmentRejectsUnknownValues(t *testing.T) {
	// A genuine typo must be a hard startup error rather than a silent
	// fall-through to "not production".
	for _, raw := range []string{"pord", "live", "stagingg", "1", "prodction", "devlopment"} {
		if _, err := ParseEnvironment(raw); err == nil {
			t.Errorf("ParseEnvironment(%q) = nil error, want rejection", raw)
		}
	}
}

func TestParseEnvironmentIsLenientAboutCaseAndWhitespace(t *testing.T) {
	// These used to silently disable every production guard. They must now
	// resolve to production rather than being rejected, because a reject here
	// would be a startup failure an operator could "fix" by spelling it
	// wronger. Getting the guard to actually apply is what matters.
	for _, raw := range []string{"prod ", " PROD", "Production", "\tPRODUCTION\n"} {
		got, err := ParseEnvironment(raw)
		if err != nil {
			t.Errorf("ParseEnvironment(%q) error = %v, want production", raw, err)
			continue
		}
		if got != EnvProduction {
			t.Errorf("ParseEnvironment(%q) = %q, want production", raw, got)
		}
	}
}

func TestParseEnvironmentNearMissesStillGetProdGuards(t *testing.T) {
	// The exact old bypass: these values reached the old `== "production"`
	// comparison as false, disabling the JWT/sslmode/gateway guards.
	for _, raw := range []string{"prod", "PROD", "Production", "production "} {
		c := prodConfig(composeFallbackSecret)
		c.Environment = raw
		if err := c.Validate(); err == nil {
			t.Errorf("APP_ENV=%q disabled the production guards", raw)
		}
	}
}

func TestParseEnvironmentAcceptsKnownAliases(t *testing.T) {
	cases := map[string]Environment{
		"":            EnvDevelopment,
		"dev":         EnvDevelopment,
		"local":       EnvDevelopment,
		"development": EnvDevelopment,
		" staging ":   EnvStaging,
		"stage":       EnvStaging,
		"prod":        EnvProduction,
		"PRODUCTION":  EnvProduction,
	}
	for raw, want := range cases {
		got, err := ParseEnvironment(raw)
		if err != nil {
			t.Errorf("ParseEnvironment(%q) error = %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("ParseEnvironment(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestStagingCountsAsProduction(t *testing.T) {
	// Staging is internet-facing: it must not ship a plaintext refresh
	// cookie, a sandbox gateway, or a dev JWT secret.
	if !EnvStaging.IsProduction() {
		t.Fatal("staging must be treated as production for safety guards")
	}
	c := prodConfig(composeFallbackSecret)
	c.Environment = "staging"
	if err := c.Validate(); err == nil {
		t.Fatal("staging accepted a published JWT secret")
	}
}

func TestValidateNormalisesEnvironment(t *testing.T) {
	c := prodConfig("kR3!xQm2#Lp9vZt7@Nw4&cYb6^fDh8*jS1aEg5+Ui3oPl9=kZ2xCv7bNm4Qw")
	c.Environment = "  PROD  "
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Environment != string(EnvProduction) {
		t.Fatalf("Environment = %q, want %q (normalised)", c.Environment, EnvProduction)
	}
	if c.IsProd() != true {
		t.Fatal("IsProd() = false after normalisation to production")
	}
}

func TestValidateRejectsSandboxGatewayInProduction(t *testing.T) {
	c := prodConfig("kR3!xQm2#Lp9vZt7@Nw4&cYb6^fDh8*jS1aEg5+Ui3oPl9=kZ2xCv7bNm4Qw")
	c.Payments.Gateway = "sandbox"
	if err := c.Validate(); err == nil {
		t.Fatal("production accepted PAYMENT_GATEWAY=sandbox")
	}
}

func TestValidateRejectsDevDBPasswordInProduction(t *testing.T) {
	c := prodConfig("kR3!xQm2#Lp9vZt7@Nw4&cYb6^fDh8*jS1aEg5+Ui3oPl9=kZ2xCv7bNm4Qw")
	c.Database.Password = "vincom_dev"
	if err := c.Validate(); err == nil {
		t.Fatal("production accepted the committed development DB password")
	}
}

func TestValidateRejectsSSLDisabledInProduction(t *testing.T) {
	c := prodConfig("kR3!xQm2#Lp9vZt7@Nw4&cYb6^fDh8*jS1aEg5+Ui3oPl9=kZ2xCv7bNm4Qw")
	c.Database.SSLMode = "disable"
	if err := c.Validate(); err == nil {
		t.Fatal("production accepted DB_SSL_MODE=disable")
	}
}
