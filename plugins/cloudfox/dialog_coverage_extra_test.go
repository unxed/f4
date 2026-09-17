package cloudfox

import (
	"encoding/json"
	"reflect"
	"testing"
)

func cloudDialogConnection(t *testing.T, provider ProviderType, settings any) Connection {
	t.Helper()
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	return Connection{Name: "coverage", Provider: provider, Settings: raw}
}

func TestCloudDialogSecretMergeAndOAuthAudienceContracts(t *testing.T) {
	base := SecretValues{"keep": "old", "replace": "old"}
	merged := mergeSecretValues(base, SecretValues{"replace": "new", "added": "value"})
	if !reflect.DeepEqual(merged, SecretValues{"keep": "old", "replace": "new", "added": "value"}) {
		t.Fatalf("merged secrets = %#v", merged)
	}
	if base["replace"] != "old" {
		t.Fatal("mergeSecretValues mutated its base map")
	}

	stored := SecretValues{"client_secret": "stored", "access_token": "stale"}
	if got := googleAuthorizationSecrets(stored, nil, false); got["client_secret"] != "stored" || got["access_token"] != "" {
		t.Fatalf("same-audience authorization secrets = %#v", got)
	}
	if got := googleAuthorizationSecrets(stored, SecretValues{"client_secret": "typed"}, true); got["client_secret"] != "typed" || len(got) != 1 {
		t.Fatalf("changed-audience authorization secrets = %#v", got)
	}

	connection := cloudDialogConnection(t, ProviderGoogleDrive, GoogleDriveSettings{ClientID: "client-a"})
	staged := SecretValues{"client_secret": "typed"}
	if got, err := googleStagedSecretsForConnection(connection, staged, "client-a"); err != nil || !reflect.DeepEqual(got, staged) {
		t.Fatalf("matching staged secrets = %#v, err=%v", got, err)
	}
	if got, err := googleStagedSecretsForConnection(connection, staged, "client-b"); err != nil || got != nil {
		t.Fatalf("mismatched staged secrets = %#v, err=%v; want nil", got, err)
	}
}

func TestCloudDialogRequiredSecretValidationAndSanitization(t *testing.T) {
	for _, tc := range []struct {
		name     string
		conn     Connection
		missing  SecretValues
		complete SecretValues
	}{
		{
			name:    "google",
			conn:    cloudDialogConnection(t, ProviderGoogleDrive, GoogleDriveSettings{ClientID: "id"}),
			missing: SecretValues{}, complete: SecretValues{"refresh_token": "refresh"},
		},
		{
			name:    "yandex",
			conn:    cloudDialogConnection(t, ProviderYandexDisk, YandexDiskSettings{ClientID: "id", Root: "disk:/"}),
			missing: SecretValues{}, complete: SecretValues{"oauth_token": "token"},
		},
		{
			name:     "s3 static",
			conn:     cloudDialogConnection(t, ProviderS3, S3Settings{Auth: "static"}),
			missing:  SecretValues{"access_key_id": "only"},
			complete: SecretValues{"access_key_id": "key", "secret_access_key": "secret"},
		},
		{
			name:    "webdav bearer",
			conn:    cloudDialogConnection(t, ProviderWebDAV, WebDAVSettings{Auth: "bearer"}),
			missing: SecretValues{}, complete: SecretValues{"bearer_token": "token"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRequiredSecrets(tc.conn, tc.missing); err == nil {
				t.Fatal("missing credentials were accepted")
			}
			if err := validateRequiredSecrets(tc.conn, tc.complete); err != nil {
				t.Fatalf("complete credentials rejected: %v", err)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		conn   Connection
		values SecretValues
		keep   string
		remove string
	}{
		{
			name: "s3 default", conn: cloudDialogConnection(t, ProviderS3, S3Settings{Auth: "default"}),
			values: SecretValues{"access_key_id": "key", "secret_access_key": "secret"}, remove: "access_key_id",
		},
		{
			name: "webdav bearer", conn: cloudDialogConnection(t, ProviderWebDAV, WebDAVSettings{Auth: "bearer"}),
			values: SecretValues{"password": "wrong", "bearer_token": "right"}, keep: "bearer_token", remove: "password",
		},
		{
			name: "webdav anonymous", conn: cloudDialogConnection(t, ProviderWebDAV, WebDAVSettings{Auth: "anonymous"}),
			values: SecretValues{"password": "wrong", "bearer_token": "wrong"}, remove: "password",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sanitizeSecretsForConnection(tc.conn, tc.values)
			if tc.values[tc.remove] != "" {
				t.Fatalf("sanitized %s credentials = %#v", tc.remove, tc.values)
			}
			if tc.keep != "" && tc.values[tc.keep] == "" {
				t.Fatalf("sanitization removed %s: %#v", tc.keep, tc.values)
			}
		})
	}
}

func TestCloudDialogProviderLabelsCoverBuiltInAndUnknownProviders(t *testing.T) {
	for _, tc := range []struct {
		provider ProviderType
		want     string
	}{
		{ProviderGoogleDrive, "Google Drive"},
		{ProviderYandexDisk, "Yandex.Disk"},
		{ProviderS3, "Amazon S3 / S3-compatible"},
		{ProviderWebDAV, "WebDAV"},
		{ProviderType("custom"), "custom"},
	} {
		if got := providerLabel(tc.provider); got != tc.want {
			t.Errorf("providerLabel(%q) = %q, want %q", tc.provider, got, tc.want)
		}
	}
}
