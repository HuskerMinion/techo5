package locale

// Update dialog keys are shared by the native firmware-updater screen. English is the fallback,
// matching the rest of the screen when no supported language is selected.
const (
	UpdateSystemTitle = "system_title"
	UpdateFailedTitle = "failed_title"
	UpdateDownloading = "downloading"
	UpdateInstalling  = "installing"
	// A binary swap restarts this process; a slot device reboots into the rootfs it unpacked. The two
	// are worded apart so the screen does not say a restart is a reboot.
	UpdateRestartingIn = "restarting_in"
	UpdateRestarting   = "restarting"
	UpdateRebootingIn  = "rebooting_in"
	UpdateRebooting    = "rebooting"
	UpdatePowerHint    = "power_hint"
	UpdateRetryHint    = "retry_hint"
)

var updateEnglish = map[string]string{
	UpdateSystemTitle: "System update",
	UpdateFailedTitle: "Update failed",
	UpdateDownloading: "Downloading update · %d%%",
	UpdateInstalling:  "Installing…",
	// The restart is of the app itself, on a device that stays up.
	UpdateRestartingIn: "Restarting in %d…",
	UpdateRestarting:   "Restarting…",
	UpdateRebootingIn:  "Rebooting in %d…",
	UpdateRebooting:    "Rebooting…",
	UpdatePowerHint:    "Keep the device powered on",
	UpdateRetryHint:    "Retry from Settings > General > Updates",
}

var updateTranslations = map[string]map[string]string{
	"de": {
		UpdateSystemTitle:  "Systemupdate",
		UpdateFailedTitle:  "Aktualisierung fehlgeschlagen",
		UpdateDownloading:  "Update wird heruntergeladen · %d%%",
		UpdateInstalling:   "Wird installiert…",
		UpdateRestartingIn: "App wird neu gestartet in %d…",
		UpdateRestarting:   "App wird neu gestartet…",
		UpdateRebootingIn:  "Neustart in %d…",
		UpdateRebooting:    "Neustart…",
		UpdatePowerHint:    "Gerät eingeschaltet lassen",
		UpdateRetryHint:    "Erneut versuchen: Settings > General > Updates",
	},
	"es": {
		UpdateSystemTitle:  "Actualización del sistema",
		UpdateFailedTitle:  "Actualización fallida",
		UpdateDownloading:  "Descargando actualización · %d%%",
		UpdateInstalling:   "Instalando…",
		UpdateRestartingIn: "La aplicación se reinicia en %d…",
		UpdateRestarting:   "Reiniciando la aplicación…",
		UpdateRebootingIn:  "Reinicio en %d…",
		UpdateRebooting:    "Reiniciando…",
		UpdatePowerHint:    "Mantén el dispositivo encendido",
		UpdateRetryHint:    "Reintenta en Settings > General > Updates",
	},
	"fr": {
		UpdateSystemTitle:  "Mise à jour du système",
		UpdateFailedTitle:  "Échec de la mise à jour",
		UpdateDownloading:  "Téléchargement de la mise à jour · %d%%",
		UpdateInstalling:   "Installation…",
		UpdateRestartingIn: "Redémarrage de l’application dans %d…",
		UpdateRestarting:   "Redémarrage de l’application…",
		UpdateRebootingIn:  "Redémarrage dans %d…",
		UpdateRebooting:    "Redémarrage…",
		UpdatePowerHint:    "Gardez l’appareil allumé",
		UpdateRetryHint:    "Réessayez dans Settings > General > Updates",
	},
	"it": {
		UpdateSystemTitle:  "Aggiornamento del sistema",
		UpdateFailedTitle:  "Aggiornamento non riuscito",
		UpdateDownloading:  "Download dell’aggiornamento · %d%%",
		UpdateInstalling:   "Installazione…",
		UpdateRestartingIn: "L’app si riavvia tra %d…",
		UpdateRestarting:   "Riavvio dell’app…",
		UpdateRebootingIn:  "Riavvio tra %d…",
		UpdateRebooting:    "Riavvio…",
		UpdatePowerHint:    "Tieni il dispositivo acceso",
		UpdateRetryHint:    "Riprova in Settings > General > Updates",
	},
	"nl": {
		UpdateSystemTitle:  "Systeemupdate",
		UpdateFailedTitle:  "Update mislukt",
		UpdateDownloading:  "Update downloaden · %d%%",
		UpdateInstalling:   "Installeren…",
		UpdateRestartingIn: "App wordt herstart over %d…",
		UpdateRestarting:   "App wordt herstart…",
		UpdateRebootingIn:  "Herstart over %d…",
		UpdateRebooting:    "Herstarten…",
		UpdatePowerHint:    "Laat het apparaat aan",
		UpdateRetryHint:    "Opnieuw proberen via Settings > General > Updates",
	},
	"pt": {
		UpdateSystemTitle:  "Atualização do sistema",
		UpdateFailedTitle:  "Falha na atualização",
		UpdateDownloading:  "Baixando atualização · %d%%",
		UpdateInstalling:   "Instalando…",
		UpdateRestartingIn: "O app reinicia em %d…",
		UpdateRestarting:   "Reiniciando o app…",
		UpdateRebootingIn:  "O aparelho reinicia em %d…",
		UpdateRebooting:    "Reiniciando o aparelho…",
		UpdatePowerHint:    "Mantenha o aparelho ligado",
		UpdateRetryHint:    "Tente de novo em Settings > General > Updates",
	},
	"ru": {
		UpdateSystemTitle:  "Обновление системы",
		UpdateFailedTitle:  "Ошибка обновления",
		UpdateDownloading:  "Загрузка обновления · %d%%",
		UpdateInstalling:   "Установка…",
		UpdateRestartingIn: "Приложение перезапустится через %d…",
		UpdateRestarting:   "Перезапуск приложения…",
		UpdateRebootingIn:  "Перезагрузка через %d…",
		UpdateRebooting:    "Перезагрузка…",
		UpdatePowerHint:    "Не выключайте устройство",
		UpdateRetryHint:    "Повторите попытку: Settings > General > Updates",
	},
}

// UpdateText returns one string from the update dialog in the requested screen language. English is
// used for English, empty or unsupported languages, and unknown keys are returned as-is so a missing
// translation cannot silently render an empty label.
func UpdateText(lang, key string) string {
	if translated, ok := updateTranslations[lang][key]; ok {
		return translated
	}
	if english, ok := updateEnglish[key]; ok {
		return english
	}
	return key
}
