package cloudfox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
	"golang.org/x/oauth2"
)

// simpleProfileEditor provides complete built-in profile editing while keeping
// provider credentials out of metadata and terminal history. Password/token
// fields are masked and intentionally start empty when an existing profile is
// edited: an empty field means "keep the stored value".
type simpleProfileEditor struct{ plugin *Plugin }

const s3BucketDiscoveryHint = "Blank discovers buckets (s3:ListAllMyBuckets); enter Bucket if denied."

func (e *simpleProfileEditor) EditProfile(app vfs.App, manager *ManagerVFS, existing *Connection) {
	if app == nil || e == nil || e.plugin == nil {
		return
	}
	if existing != nil {
		showCloudProfileDialog(app, manager, e.plugin, existing.Provider, existing)
		return
	}
	providers := e.plugin.Providers()
	if len(providers) == 0 {
		app.Message(manager.strings.ErrorTitle, ErrFactoryNotRegistered.Error(), []string{"&OK"})
		return
	}
	labels := make([]string, len(providers))
	for i, provider := range providers {
		labels[i] = providerLabel(provider)
	}
	app.Menu(manager.strings.ChooseType, labels, func(index int) {
		if index >= 0 && index < len(providers) {
			showCloudProfileDialog(app, manager, e.plugin, providers[index], nil)
		}
	})
}

type cloudProfileDialog struct {
	app      vfs.App
	manager  *ManagerVFS
	plugin   *Plugin
	provider ProviderType
	original *Connection
	dialog   *vtui.Window

	name    *vtui.Edit
	storage *vtui.ComboBox
	fields  map[string]*vtui.Edit
	combos  map[string]*vtui.ComboBox
	checks  map[string]*vtui.Checkbox
	status  *vtui.Text
	action  *vtui.Button
	save    *vtui.Button
	staged  SecretValues
	// stagedGoogleClientID binds an authorization result to the visible OAuth
	// audience. It prevents tokens obtained for one client ID from being saved
	// after the user edits the client ID without authorizing again.
	stagedGoogleClientID string
	running              *vtui.TaskContext
	row                  int
	fieldX               int
	fieldWidth           int
}

func showCloudProfileDialog(app vfs.App, manager *ManagerVFS, plugin *Plugin, provider ProviderType, existing *Connection) {
	d := &cloudProfileDialog{
		app: app, manager: manager, plugin: plugin, provider: provider,
		fields: make(map[string]*vtui.Edit), combos: make(map[string]*vtui.ComboBox), checks: make(map[string]*vtui.Checkbox),
	}
	if existing != nil {
		clone := existing.Clone()
		d.original = &clone
	}
	d.dialog = vtui.NewCenteredDialog(78, 24, " "+fmt.Sprintf(vtui.Msg("CloudFox.ProfileTitle"), providerLabel(provider))+" ")
	d.dialog.ShowClose = true
	d.fieldX = d.dialog.X1 + 23
	d.fieldWidth = 51
	d.row = d.dialog.Y1 + 2

	name := ""
	if existing != nil {
		name = existing.Name
	}
	d.name = d.addEdit("name", "&Name:", name, false)
	d.addReadOnly("Provider:", providerLabel(provider))
	storageItems := []string{"System keyring", "Encrypted vault"}
	storageIndex := 0
	if plugin.portable {
		storageItems = []string{"Encrypted vault"}
	} else if existing != nil && strings.HasPrefix(existing.SecretRef, "vault:") {
		storageIndex = 1
	}
	d.storage = d.addCombo("storage", "Secret &storage:", storageItems, storageIndex)

	switch provider {
	case ProviderGoogleDrive:
		d.buildGoogleFields(existing)
	case ProviderYandexDisk:
		d.buildYandexFields(existing)
	case ProviderS3:
		d.buildS3Fields(existing)
	case ProviderWebDAV:
		d.buildWebDAVFields(existing)
	default:
		app.Message(manager.strings.ErrorTitle, fmt.Sprintf("Unsupported provider %q", provider), []string{"&OK"})
		return
	}

	statusText := "Credentials are not stored in the profile file."
	if existing != nil && existing.SecretRef != "" {
		statusText = "Stored credentials stay unless server/account scope changes."
	}
	d.status = vtui.NewText(d.dialog.X1+2, d.dialog.Y2-4, statusText, vtui.Palette[vtui.ColDialogText])
	d.status.SetPosition(d.dialog.X1+2, d.dialog.Y2-4, d.dialog.X2-2, d.dialog.Y2-4)
	d.dialog.AddItem(d.status)

	actionLabel := "&Test"
	if provider == ProviderGoogleDrive || provider == ProviderYandexDisk {
		actionLabel = "&Authorize"
	}
	d.action = vtui.NewButton(d.dialog.X1+18, d.dialog.Y2-2, actionLabel)
	d.save = vtui.NewButton(d.dialog.X1+32, d.dialog.Y2-2, vtui.Msg("vtui.Save"))
	d.save.IsDefault = true
	cancel := vtui.NewButton(d.dialog.X1+44, d.dialog.Y2-2, vtui.Msg("vtui.Cancel"))
	d.dialog.AddItem(d.action)
	d.dialog.AddItem(d.save)
	d.dialog.AddItem(cancel)
	d.action.OnClick = d.onAction
	d.save.OnClick = d.onSave
	cancel.OnClick = d.dialog.Close
	d.dialog.OnResult = func(int) {
		if d.running != nil {
			d.running.Cancel()
		}
	}
	d.dialog.SetFocusedItem(d.name)
	vtui.FrameManager.Push(d.dialog)
}

func (d *cloudProfileDialog) addEdit(id, label, initial string, password bool) *vtui.Edit {
	var edit *vtui.Edit
	if password {
		edit = vtui.NewPasswordEdit(d.fieldX, d.row, d.fieldWidth, initial)
	} else {
		edit = vtui.NewEdit(d.fieldX, d.row, d.fieldWidth, initial)
	}
	d.dialog.AddItem(vtui.NewLabel(d.dialog.X1+2, d.row, label, edit))
	d.dialog.AddItem(edit)
	d.fields[id] = edit
	d.row++
	return edit
}

func (d *cloudProfileDialog) addCombo(id, label string, items []string, selected int) *vtui.ComboBox {
	combo := vtui.NewComboBox(d.fieldX, d.row, d.fieldWidth, items)
	combo.DropdownOnly = true
	if selected < 0 || selected >= len(items) {
		selected = 0
	}
	combo.Menu.SetSelectPos(selected)
	if len(items) != 0 {
		combo.Edit.SetText(items[selected])
	}
	d.dialog.AddItem(vtui.NewLabel(d.dialog.X1+2, d.row, label, combo))
	d.dialog.AddItem(combo)
	d.combos[id] = combo
	d.row++
	return combo
}

func (d *cloudProfileDialog) addCheck(id, label string, checked bool) *vtui.Checkbox {
	check := vtui.NewCheckbox(d.fieldX, d.row, label, false)
	check.SetData(checked)
	d.dialog.AddItem(check)
	d.checks[id] = check
	d.row++
	return check
}

func (d *cloudProfileDialog) addReadOnly(label, value string) {
	d.dialog.AddItem(vtui.NewText(d.dialog.X1+2, d.row, label, vtui.Palette[vtui.ColDialogText]))
	d.dialog.AddItem(vtui.NewText(d.fieldX, d.row, value, vtui.Palette[vtui.ColDialogText]))
	d.row++
}

func (d *cloudProfileDialog) addHint(value string) {
	hint := vtui.NewText(d.dialog.X1+2, d.row, value, 0)
	hint.SetPosition(d.dialog.X1+2, d.row, d.dialog.X2-2, d.row)
	d.dialog.AddItem(hint)
	d.row++
}

func (d *cloudProfileDialog) buildGoogleFields(existing *Connection) {
	settings := GoogleDriveSettings{ClientID: DefaultGoogleClientID}
	if existing != nil {
		_ = json.Unmarshal(existing.Settings, &settings)
	}
	d.addEdit("client_id", "OAuth client &ID:", settings.ClientID, false)
	d.addEdit("client_secret", "Client secret:", "", true)
	d.addReadOnly("Sign-in:", "Desktop loopback OAuth with PKCE")
}

func (d *cloudProfileDialog) buildYandexFields(existing *Connection) {
	settings := YandexDiskSettings{ClientID: DefaultYandexClientID, Root: "disk:/"}
	if existing != nil {
		_ = json.Unmarshal(existing.Settings, &settings)
	}
	d.addEdit("client_id", "OAuth client &ID:", settings.ClientID, false)
	d.addEdit("root", "Remote &root:", settings.Root, false)
	d.addEdit("oauth_token", "OAuth token (optional):", "", true)
	d.addReadOnly("Sign-in:", "Browser authorization code with PKCE")
}

func (d *cloudProfileDialog) buildS3Fields(existing *Connection) {
	settings := S3Settings{Region: "us-east-1", Auth: "default"}
	if existing != nil {
		_ = json.Unmarshal(existing.Settings, &settings)
	}
	authItems := []string{"default", "profile", "static", "anonymous"}
	d.addEdit("bucket", "&Bucket (optional):", settings.Bucket, false)
	d.addHint(s3BucketDiscoveryHint)
	d.addEdit("region", "&Region:", settings.Region, false)
	d.addEdit("root_prefix", "Root prefi&x:", settings.RootPrefix, false)
	d.addEdit("endpoint", "Custom endpoint:", settings.Endpoint, false)
	auth := d.addCombo("auth", "&Authentication:", authItems, stringIndex(authItems, settings.Auth))
	d.addEdit("profile", "AWS profile:", settings.Profile, false)
	accessKey := d.addEdit("access_key_id", "Access key ID:", "", true)
	secretKey := d.addEdit("secret_access_key", "Secret access key:", "", true)
	sessionToken := d.addEdit("session_token", "Session token:", "", true)
	staticIndex := stringIndex(authItems, "static")
	selectStaticAuthentication := func(string) {
		if !d.hasS3StaticSecretInput() {
			return
		}
		auth.Menu.SetSelectPos(staticIndex)
		auth.Edit.SetText(authItems[staticIndex])
	}
	accessKey.OnTextChange = selectStaticAuthentication
	secretKey.OnTextChange = selectStaticAuthentication
	sessionToken.OnTextChange = selectStaticAuthentication
	d.addEdit("custom_ca", "Custom CA file:", settings.CustomCA, false)
	d.addCheck("path_style", "Use &path-style addressing", settings.UsePathStyle)
	d.addCheck("insecure_s3", "Allow credentials over plain &HTTP", settings.AllowInsecure)
}

func (d *cloudProfileDialog) buildWebDAVFields(existing *Connection) {
	settings := WebDAVSettings{Root: "/", Auth: "basic"}
	if existing != nil {
		_ = json.Unmarshal(existing.Settings, &settings)
	}
	authItems := []string{"basic", "digest", "bearer", "anonymous"}
	d.addEdit("base_url", "Server &URL:", settings.BaseURL, false)
	d.addEdit("root", "Remote &root:", settings.Root, false)
	d.addCombo("auth", "&Authentication:", authItems, stringIndex(authItems, settings.Auth))
	d.addEdit("username", "Username:", settings.Username, false)
	d.addEdit("credential", "Password / token:", "", true)
	d.addEdit("custom_ca", "Custom CA file:", settings.CustomCA, false)
	d.addCheck("insecure_digest", "Allow Digest over plain &HTTP", settings.AllowInsecureDigest)
}

func stringIndex(values []string, value string) int {
	for index, candidate := range values {
		if strings.EqualFold(candidate, value) {
			return index
		}
	}
	return 0
}

func (d *cloudProfileDialog) value(id string) string {
	if edit := d.fields[id]; edit != nil {
		return strings.TrimSpace(edit.GetText())
	}
	return ""
}

func (d *cloudProfileDialog) secretValue(id string) string {
	if edit := d.fields[id]; edit != nil {
		return edit.GetText()
	}
	return ""
}

func (d *cloudProfileDialog) comboValue(id string) string {
	if combo := d.combos[id]; combo != nil {
		return strings.ToLower(strings.TrimSpace(combo.Edit.GetText()))
	}
	return ""
}

func (d *cloudProfileDialog) hasS3StaticSecretInput() bool {
	return d.secretValue("access_key_id") != "" ||
		d.secretValue("secret_access_key") != "" ||
		d.secretValue("session_token") != ""
}

func (d *cloudProfileDialog) checked(id string) bool {
	return d.checks[id] != nil && d.checks[id].State == 1
}

func (d *cloudProfileDialog) selectedStorage() SecretStorage {
	if d.plugin.portable || d.storage == nil || strings.Contains(strings.ToLower(d.storage.Edit.GetText()), "vault") {
		return SecretStorageVault
	}
	return SecretStorageKeyring
}

func (d *cloudProfileDialog) connectionSnapshot() (Connection, error) {
	connection := Connection{Name: d.value("name"), Provider: d.provider}
	if d.original != nil {
		connection = d.original.Clone()
		connection.Name = d.value("name")
	}
	var settings any
	switch d.provider {
	case ProviderGoogleDrive:
		settings = GoogleDriveSettings{ClientID: d.value("client_id")}
	case ProviderYandexDisk:
		settings = YandexDiskSettings{ClientID: d.value("client_id"), Root: d.value("root")}
	case ProviderS3:
		auth := d.comboValue("auth")
		// Access-key fields are meaningful only for static authentication. Do
		// not silently discard credentials merely because the profile dialog
		// started with the default AWS credential chain selected.
		if d.hasS3StaticSecretInput() {
			auth = "static"
		}
		settings = S3Settings{
			Bucket: d.value("bucket"), Region: d.value("region"), RootPrefix: d.value("root_prefix"), Endpoint: d.value("endpoint"),
			UsePathStyle: d.checked("path_style"), AllowInsecure: d.checked("insecure_s3"), Auth: auth, Profile: d.value("profile"), CustomCA: d.value("custom_ca"),
		}
	case ProviderWebDAV:
		settings = WebDAVSettings{
			BaseURL: d.value("base_url"), Root: d.value("root"), Auth: d.comboValue("auth"), Username: d.value("username"),
			CustomCA: d.value("custom_ca"), AllowInsecureDigest: d.checked("insecure_digest"),
		}
	default:
		return Connection{}, fmt.Errorf("cloudfox: unsupported provider %q", d.provider)
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return Connection{}, err
	}
	connection.Settings = raw
	factory, ok := d.plugin.Factory(d.provider)
	if !ok {
		return Connection{}, ErrFactoryNotRegistered
	}
	if err := factory.Validate(connection); err != nil {
		return Connection{}, err
	}
	return connection, nil
}

func (d *cloudProfileDialog) typedSecrets() (SecretValues, bool) {
	values := SecretValues{}
	set := func(field, key string) {
		if value := d.secretValue(field); value != "" {
			values[key] = value
		}
	}
	switch d.provider {
	case ProviderGoogleDrive:
		set("client_secret", "client_secret")
	case ProviderYandexDisk:
		set("oauth_token", "oauth_token")
	case ProviderS3:
		set("access_key_id", "access_key_id")
		set("secret_access_key", "secret_access_key")
		set("session_token", "session_token")
	case ProviderWebDAV:
		if value := d.secretValue("credential"); value != "" {
			if d.comboValue("auth") == "bearer" {
				values["bearer_token"] = value
			} else {
				values["password"] = value
			}
		}
	}
	return values, len(values) != 0
}

func mergeSecretValues(base, overlay SecretValues) SecretValues {
	merged := base.Clone()
	if merged == nil {
		merged = SecretValues{}
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

func googleAuthorizationSecrets(stored, typed SecretValues, audienceChanged bool) SecretValues {
	values := SecretValues{}
	// A stored client secret is usable only for the same OAuth client. Access
	// and refresh tokens are never inputs to a fresh authorization and retaining
	// them can mix accounts when Google omits a replacement refresh token.
	if !audienceChanged && stored["client_secret"] != "" {
		values["client_secret"] = stored["client_secret"]
	}
	if typed["client_secret"] != "" {
		values["client_secret"] = typed["client_secret"]
	}
	return values
}

func (d *cloudProfileDialog) googleSecretsForAuthorization(ctx context.Context, connection Connection, typed SecretValues) (SecretValues, error) {
	audienceChanged, err := credentialScopeChanged(d.original, connection)
	if err != nil {
		return nil, err
	}
	stored := SecretValues{}
	if d.original != nil && d.original.SecretRef != "" && !audienceChanged {
		stored, err = d.existingSecrets(ctx)
		if err != nil {
			return nil, err
		}
	}
	return googleAuthorizationSecrets(stored, typed, audienceChanged), nil
}

func googleClientID(connection Connection) (string, error) {
	settings, err := (&GoogleDriveFactory{}).settings(connection)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(settings.ClientID), nil
}

func googleStagedSecretsForConnection(connection Connection, staged SecretValues, stagedClientID string) (SecretValues, error) {
	if len(staged) == 0 {
		return nil, nil
	}
	clientID, err := googleClientID(connection)
	if err != nil {
		return nil, err
	}
	if stagedClientID == "" || stagedClientID != clientID {
		return nil, nil
	}
	return staged.Clone(), nil
}

func sanitizeSecretsForConnection(connection Connection, values SecretValues) {
	switch connection.Provider {
	case ProviderS3:
		var settings S3Settings
		_ = json.Unmarshal(connection.Settings, &settings)
		if settings.Auth != "static" {
			delete(values, "access_key_id")
			delete(values, "secret_access_key")
			delete(values, "session_token")
		}
	case ProviderWebDAV:
		var settings WebDAVSettings
		_ = json.Unmarshal(connection.Settings, &settings)
		switch settings.Auth {
		case "anonymous":
			delete(values, "password")
			delete(values, "bearer_token")
		case "bearer":
			delete(values, "password")
		default:
			delete(values, "bearer_token")
		}
	}
}

func validateCredentialScopeChange(original *Connection, updated Connection, typed SecretValues) (bool, error) {
	changed, err := credentialScopeChanged(original, updated)
	if err != nil || !changed || !credentialScopeNeedsStoredSecret(updated) {
		return changed, err
	}
	if err := validateRequiredSecrets(updated, typed); err != nil {
		return true, fmt.Errorf("credential destination or identity changed; re-enter credentials before continuing: %w", err)
	}
	return true, nil
}

func isCredentialScopeBindingError(err error) bool {
	return errors.Is(err, ErrCredentialScopeUnbound) || errors.Is(err, ErrCredentialScopeMismatch)
}

func validateRequiredSecrets(connection Connection, values SecretValues) error {
	switch connection.Provider {
	case ProviderGoogleDrive:
		if values["refresh_token"] == "" && values["access_token"] == "" {
			return errors.New("authorize the Google Drive profile before saving")
		}
	case ProviderYandexDisk:
		if values["oauth_token"] == "" && values["access_token"] == "" {
			return errors.New("authorize the Yandex.Disk profile or enter an OAuth token")
		}
	case ProviderS3:
		var settings S3Settings
		_ = json.Unmarshal(connection.Settings, &settings)
		if settings.Auth == "static" && (values["access_key_id"] == "" || values["secret_access_key"] == "") {
			return errors.New("static S3 authentication requires an access key ID and secret access key")
		}
	case ProviderWebDAV:
		var settings WebDAVSettings
		_ = json.Unmarshal(connection.Settings, &settings)
		if settings.Auth == "anonymous" {
			return nil
		}
		if settings.Auth == "bearer" && values["bearer_token"] == "" {
			return errors.New("WebDAV Bearer authentication requires a token")
		}
		if settings.Auth != "bearer" && values["password"] == "" {
			return errors.New("WebDAV authentication requires a password")
		}
	}
	return nil
}

func (d *cloudProfileDialog) existingSecrets(ctx context.Context) (SecretValues, error) {
	if d.original == nil || d.original.SecretRef == "" {
		return SecretValues{}, nil
	}
	return d.plugin.repo.Credentials(ctx, *d.original)
}

func (d *cloudProfileDialog) setBusy(busy bool, text string) {
	d.action.SetDisabled(busy)
	d.save.SetDisabled(busy)
	if text != "" {
		d.status.SetText(text)
	}
	vtui.FrameManager.Redraw()
}

func (d *cloudProfileDialog) start(text string, worker func(*vtui.TaskContext)) {
	if d.running != nil {
		return
	}
	d.setBusy(true, text)
	d.running = vtui.RunAsync(func(task *vtui.TaskContext) {
		defer task.RunOnUI(func() {
			if !d.dialog.IsDone() {
				d.running = nil
				d.setBusy(false, "")
			}
		})
		worker(task)
	})
}

func (d *cloudProfileDialog) showError(err error) {
	if err == nil || d.dialog.IsDone() {
		return
	}
	vtui.ShowMessageOn(d.dialog, " CloudFox ", err.Error(), []string{"&OK"})
}

func (d *cloudProfileDialog) onAction() {
	connection, err := d.connectionSnapshot()
	if err != nil {
		d.showError(err)
		return
	}
	typed, _ := d.typedSecrets()
	switch d.provider {
	case ProviderGoogleDrive:
		d.start("Waiting for Google authorization in the browser...", func(task *vtui.TaskContext) {
			stored, err := d.googleSecretsForAuthorization(task, connection, typed)
			if err == nil {
				var cancel context.CancelFunc
				ctx := context.Context(task)
				ctx, cancel = context.WithTimeout(ctx, 10*time.Minute)
				defer cancel()
				stored, err = AuthorizeGoogleDesktop(ctx, connection, stored, nil)
			}
			task.RunOnUI(func() {
				if err != nil {
					d.showError(err)
					return
				}
				d.staged = stored
				d.stagedGoogleClientID, _ = googleClientID(connection)
				d.fields["client_secret"].SetText("")
				d.status.SetText("Google Drive authorization succeeded; press Save.")
			})
		})
	case ProviderYandexDisk:
		var settings YandexDiskSettings
		_ = json.Unmarshal(connection.Settings, &settings)
		d.start("Waiting for Yandex authorization in the browser...", func(task *vtui.TaskContext) {
			verifier := oauth2.GenerateVerifier()
			authorizationURL, err := YandexAuthorizationURL("", settings.ClientID, "", verifier)
			if err == nil {
				err = openBrowserURL(authorizationURL)
			}
			var code string
			if err == nil {
				code, err = promptYandexAuthorizationCode(task)
			}
			var token SecretValues
			if err == nil {
				token, err = ExchangeYandexAuthorizationCode(task, http.DefaultClient, "", settings.ClientID, code, verifier)
			}
			stored := SecretValues{}
			if err == nil {
				stored, err = d.existingSecrets(task)
			}
			if err == nil {
				stored = mergeSecretValues(stored, token)
			}
			task.RunOnUI(func() {
				if err != nil {
					d.showError(err)
					return
				}
				d.staged = stored
				d.fields["oauth_token"].SetText("")
				d.status.SetText("Yandex.Disk authorization succeeded; press Save.")
			})
		})
	default:
		scopeChanged, scopeErr := validateCredentialScopeChange(d.original, connection, typed)
		if scopeErr != nil {
			d.showError(scopeErr)
			return
		}
		d.start("Testing connection...", func(task *vtui.TaskContext) {
			stored := SecretValues{}
			var err error
			if d.original != nil && d.original.SecretRef != "" && !scopeChanged {
				stored, err = d.existingSecrets(task)
				if isCredentialScopeBindingError(err) {
					err = nil
					// Saving/testing the visible settings is the explicit migration
					// confirmation for external S3 credential sources. Inline
					// WebDAV/static-S3 credentials must be re-entered in full.
					if credentialScopeNeedsStoredSecret(connection) {
						err = validateRequiredSecrets(connection, typed)
					}
					if err == nil {
						stored = SecretValues{}
					}
				}
			}
			if err == nil {
				stored = mergeSecretValues(stored, d.staged)
				stored = mergeSecretValues(stored, typed)
				sanitizeSecretsForConnection(connection, stored)
				err = validateRequiredSecrets(connection, stored)
				if err == nil {
					err = verifyCredentialScope(connection, stored, false)
				}
			}
			var backend Backend
			if err == nil {
				factory, ok := d.plugin.Factory(connection.Provider)
				if !ok {
					err = ErrFactoryNotRegistered
				} else {
					backend, err = factory.Open(task, connection, stored)
				}
			}
			if err == nil {
				err = testCloudBackend(task, backend)
			}
			if backend != nil {
				_ = backend.Close()
			}
			task.RunOnUI(func() {
				if err != nil {
					d.showError(err)
					return
				}
				d.status.SetText("Connection test succeeded.")
			})
		})
	}
}

// testCloudBackend lets providers make the cheapest authoritative network
// request for their configured root. S3 uses this to distinguish bucket
// discovery (ListBuckets) from an explicitly configured bucket without making
// the generic dialog enumerate an arbitrarily large directory. Older and test
// backends retain the established root-stat fallback.
func testCloudBackend(ctx context.Context, backend Backend) error {
	if tester, ok := backend.(interface {
		TestConnection(context.Context) error
	}); ok {
		return tester.TestConnection(ctx)
	}
	_, err := backend.Stat(ctx, backend.Root())
	return err
}

func (d *cloudProfileDialog) onSave() {
	connection, err := d.connectionSnapshot()
	if err != nil {
		d.showError(err)
		return
	}
	typed, typedChanged := d.typedSecrets()
	staged := d.staged.Clone()
	if d.provider == ProviderGoogleDrive && len(staged) != 0 {
		var audienceErr error
		staged, audienceErr = googleStagedSecretsForConnection(connection, staged, d.stagedGoogleClientID)
		if audienceErr != nil {
			d.showError(audienceErr)
			return
		}
	}
	storage := d.selectedStorage()
	storageChanged := d.original != nil && d.original.SecretRef != "" &&
		((storage == SecretStorageVault) != strings.HasPrefix(d.original.SecretRef, "vault:"))
	scopeChanged, err := validateCredentialScopeChange(d.original, connection, typed)
	if err != nil {
		d.showError(err)
		return
	}
	_, scopeBindingRequired, err := credentialScope(connection)
	if err != nil {
		d.showError(err)
		return
	}
	d.start("Saving profile...", func(task *vtui.TaskContext) {
		needSecrets := typedChanged || len(staged) != 0 || storageChanged || scopeChanged || scopeBindingRequired
		var values SecretValues
		if needSecrets {
			values = SecretValues{}
			if d.original != nil && d.original.SecretRef != "" && !scopeChanged {
				values, err = d.existingSecrets(task)
				if isCredentialScopeBindingError(err) {
					err = nil
					if credentialScopeNeedsStoredSecret(connection) {
						err = validateRequiredSecrets(connection, typed)
					}
					if err == nil {
						values = SecretValues{}
					}
				}
			}
			if err == nil {
				values = mergeSecretValues(values, staged)
				values = mergeSecretValues(values, typed)
				sanitizeSecretsForConnection(connection, values)
			}
		}
		if err == nil && (d.original == nil || needSecrets) {
			candidate := values
			if candidate == nil {
				candidate = SecretValues{}
			}
			err = validateRequiredSecrets(connection, candidate)
		}
		var valuePointer *SecretValues
		if err == nil && needSecrets {
			valuePointer = &values
		}
		if err == nil {
			_, err = d.plugin.repo.Save(task, connection, valuePointer, storage)
		}
		task.RunOnUI(func() {
			if err != nil {
				d.showError(err)
				return
			}
			d.staged = nil
			d.stagedGoogleClientID = ""
			d.dialog.Close()
			d.app.RefreshAll()
		})
	})
}

func providerLabel(provider ProviderType) string {
	switch provider {
	case ProviderGoogleDrive:
		return "Google Drive"
	case ProviderYandexDisk:
		return "Yandex.Disk"
	case ProviderS3:
		return "Amazon S3 / S3-compatible"
	case ProviderWebDAV:
		return "WebDAV"
	default:
		return string(provider)
	}
}
