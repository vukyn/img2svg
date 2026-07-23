// Human-readable byte size (mirrors the original vanilla UI's `human`).
export function human(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / 1048576).toFixed(2)} MB`;
}

// Wall-clock timestamp in 24h form, matching the log's [HH:MM:SS] prefix.
export function timestamp(): string {
	return new Date().toLocaleTimeString([], { hour12: false });
}
