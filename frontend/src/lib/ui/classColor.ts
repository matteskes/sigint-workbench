// §4 design tokens: the single source for the signal-class palette.
// MapView and SignalList used to duplicate these maps; every consumer
// imports from here (§13).

/** Display order for chips/filters — `unknown` last. */
export const SIGNAL_CLASSES = [
	'aviation',
	'land_mobile',
	'marine',
	'broadcast',
	'amateur',
	'gnss',
	'wifi',
	'unknown'
] as const;

export type SignalClass = (typeof SIGNAL_CLASSES)[number];

/** Hex values for MapLibre paint properties (map dots, halos, strokes). */
export const CLASS_HEX: Record<string, string> = {
	aviation: '#3b82f6',
	land_mobile: '#22c55e',
	marine: '#06b6d4',
	broadcast: '#f59e0b',
	amateur: '#a855f7',
	gnss: '#ef4444',
	wifi: '#6366f1',
	unknown: '#64748b'
};

/** Tailwind text classes for tables and chips. */
export const CLASS_TEXT: Record<string, string> = {
	aviation: 'text-blue-400',
	land_mobile: 'text-green-400',
	marine: 'text-cyan-400',
	broadcast: 'text-amber-400',
	amateur: 'text-purple-400',
	gnss: 'text-red-400',
	wifi: 'text-indigo-400',
	unknown: 'text-slate-400'
};

/** Hex for a class, defaulting unknown classes to the `unknown` color. */
export function classHex(cls: string): string {
	return CLASS_HEX[cls] ?? CLASS_HEX.unknown;
}

/** Tailwind text class for a class, defaulting to the `unknown` class. */
export function classText(cls: string): string {
	return CLASS_TEXT[cls] ?? CLASS_TEXT.unknown;
}
