import type { Dict } from "../lib/i18n";

// English is the text of each key: only the texts that count something
// need their singular here, and the keys that read better otherwise.
const en: Dict = {
  "Total traffic, {range}": "Total traffic · {range}",
  "Download {down} · upload {up}": "Download {down} · Upload {up}",
  // Inside sentences too, as "Scanned just now".
  "Just now": "just now",
  Never: "never",
  "{n} devices found": { one: "{n} device found", other: "{n} devices found" },
  "{n} connections": { one: "{n} connection", other: "{n} connections" },
  "Expires on {date}, in {n} days": { one: "Expires on {date}, in {n} day", other: "Expires on {date}, in {n} days" },
  "↑ {up} · ↓ {n} files": { one: "↑ {up} · ↓ {n} file", other: "↑ {up} · ↓ {n} files" },
  "{n} devices running Clash Traffic found, synced every minute": {
    one: "{n} device running Clash Traffic found, synced every minute",
    other: "{n} devices running Clash Traffic found, synced every minute",
  },
  "Every {n} minutes": { one: "Every minute", other: "Every {n} minutes" },
  "{n} days": { one: "{n} day", other: "{n} days" },
};

export default en;
