package locale

// Update dialog keys are shared by the native firmware-updater screen. English is the fallback,
// matching the rest of the screen when no supported language is selected.
const (
	UpdateSystemTitle = "system_title"
	UpdateFailedTitle = "failed_title"
	UpdateDownloading = "downloading"
	UpdateInstalling  = "installing"
	UpdateRebootingIn = "rebooting_in"
	UpdateRebooting   = "rebooting"
	UpdatePowerHint   = "power_hint"
	UpdateRetryHint   = "retry_hint"
)

var updateEnglish = map[string]string{
	UpdateSystemTitle: "System update",
	UpdateFailedTitle: "Update failed",
	UpdateDownloading: "Downloading update · %d%%",
	UpdateInstalling:  "Installing…",
	UpdateRebootingIn: "Rebooting in %d…",
	UpdateRebooting:   "Rebooting…",
	UpdatePowerHint:   "Keep the device powered on",
	UpdateRetryHint:   "Retry from Settings > General > Updates",
}

var updateTranslations = map[string]map[string]string{
	"de": {
		UpdateSystemTitle: "Systemupdate",
		UpdateFailedTitle: "Aktualisierung fehlgeschlagen",
		UpdateDownloading: "Update wird heruntergeladen · %d%%",
		UpdateInstalling:  "Wird installiert…",
		UpdateRebootingIn: "Neustart in %d…",
		UpdateRebooting:   "Neustart…",
		UpdatePowerHint:   "Gerät eingeschaltet lassen",
		UpdateRetryHint:   "Erneut versuchen: Settings > General > Updates",
	},
	"es": {
		UpdateSystemTitle: "Actualización del sistema",
		UpdateFailedTitle: "Actualización fallida",
		UpdateDownloading: "Descargando actualización · %d%%",
		UpdateInstalling:  "Instalando…",
		UpdateRebootingIn: "Reinicio en %d…",
		UpdateRebooting:   "Reiniciando…",
		UpdatePowerHint:   "Mantén el dispositivo encendido",
		UpdateRetryHint:   "Reintenta en Settings > General > Updates",
	},
	"fr": {
		UpdateSystemTitle: "Mise à jour du système",
		UpdateFailedTitle: "Échec de la mise à jour",
		UpdateDownloading: "Téléchargement de la mise à jour · %d%%",
		UpdateInstalling:  "Installation…",
		UpdateRebootingIn: "Redémarrage dans %d…",
		UpdateRebooting:   "Redémarrage…",
		UpdatePowerHint:   "Gardez l’appareil allumé",
		UpdateRetryHint:   "Réessayez dans Settings > General > Updates",
	},
	"it": {
		UpdateSystemTitle: "Aggiornamento del sistema",
		UpdateFailedTitle: "Aggiornamento non riuscito",
		UpdateDownloading: "Download dell’aggiornamento · %d%%",
		UpdateInstalling:  "Installazione…",
		UpdateRebootingIn: "Riavvio tra %d…",
		UpdateRebooting:   "Riavvio…",
		UpdatePowerHint:   "Tieni il dispositivo acceso",
		UpdateRetryHint:   "Riprova in Settings > General > Updates",
	},
	"nl": {
		UpdateSystemTitle: "Systeemupdate",
		UpdateFailedTitle: "Update mislukt",
		UpdateDownloading: "Update downloaden · %d%%",
		UpdateInstalling:  "Installeren…",
		UpdateRebootingIn: "Herstart over %d…",
		UpdateRebooting:   "Herstarten…",
		UpdatePowerHint:   "Laat het apparaat aan",
		UpdateRetryHint:   "Opnieuw proberen via Settings > General > Updates",
	},
	"ru": {
		UpdateSystemTitle: "Обновление системы",
		UpdateFailedTitle: "Ошибка обновления",
		UpdateDownloading: "Загрузка обновления · %d%%",
		UpdateInstalling:  "Установка…",
		UpdateRebootingIn: "Перезагрузка через %d…",
		UpdateRebooting:   "Перезагрузка…",
		UpdatePowerHint:   "Не выключайте устройство",
		UpdateRetryHint:   "Повторите попытку: Settings > General > Updates",
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
