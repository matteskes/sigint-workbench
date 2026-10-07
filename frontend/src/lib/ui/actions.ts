/**
 * Small Svelte actions shared by the shell and route views.
 */
import type { Action } from 'svelte/action';

/** Options for {@link popoverDismiss}. */
export interface DismissOptions {
	/** Whether the popover is currently shown. */
	open: boolean;
	/** Invoked on an outside press or Escape while `open`. */
	onDismiss: () => void;
}

/**
 * Dismiss a transient popover on outside-press or Escape (§15: Esc
 * closes popovers). Attach to the popover's positioning wrapper — the
 * element spanning both the trigger and the panel — so the trigger's
 * own toggle and the panel's controls never count as "outside".
 * Listeners sit on `document` in the capture phase, so dismissal wins
 * even when another handler stops propagation, and the `open` guard
 * keeps them inert while the popover is closed.
 */
export const popoverDismiss: Action<HTMLElement, DismissOptions> = (node, initial) => {
	let opts = initial;

	function onPointerDown(e: PointerEvent): void {
		if (opts.open && e.target instanceof Node && !node.contains(e.target)) {
			opts.onDismiss();
		}
	}

	function onKeyDown(e: KeyboardEvent): void {
		if (opts.open && e.key === 'Escape') {
			opts.onDismiss();
			// UI-BUGCHECK B13: one layer per press. This capture-phase
			// listener swallows the keystroke so the shell's global Esc
			// chain (§15: shortcuts overlay → receiver card → selection)
			// doesn't close a second layer at the same time.
			e.stopImmediatePropagation();
		}
	}

	document.addEventListener('pointerdown', onPointerDown, true);
	document.addEventListener('keydown', onKeyDown, true);

	return {
		update(next: DismissOptions) {
			opts = next;
		},
		destroy() {
			document.removeEventListener('pointerdown', onPointerDown, true);
			document.removeEventListener('keydown', onKeyDown, true);
		}
	};
};
