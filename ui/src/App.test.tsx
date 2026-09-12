import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App from "./App";

// The traced SVG is handed to the views as an object URL rather than as markup,
// which puts a resource lifetime in App's hands: a URL minted per trace has to be
// released, or the blob behind it is pinned for the life of the tab. These tests
// drive the real component through a real trace and watch both ends of that.
//
// The default options are what make this possible without a canvas: with no
// resize and no transparency, prepareInput short-circuits before it touches one,
// which jsdom does not implement.

// The marker is what makes "not injected" checkable. The app renders plenty of
// legitimate inline SVG of its own — every button icon is one — so looking for
// <svg> or <path> in the document would find the toolbar, not the payload.
const MARKER = "traced-payload-marker";
const SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" id="${MARKER}"><path d="M0 0h10v10H0z"/></svg>`;

let container: HTMLDivElement;
let root: Root;
let created: string[];
let revoked: string[];

beforeEach(() => {
	(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);

	created = [];
	revoked = [];
	let serial = 0;
	// jsdom implements neither object URLs nor blob fetching, so both sides are
	// stubbed — and the stubs are the assertion surface: the point of the test is
	// which URLs get minted and which get released.
	vi.stubGlobal("URL", Object.assign(Object.create(URL), URL, {
		createObjectURL: (blob: Blob) => {
			const url = `blob:test/${blob.type || "unknown"}/${serial++}`;
			created.push(url);
			return url;
		},
		revokeObjectURL: (url: string) => {
			revoked.push(url);
		},
	}));

	vi.stubGlobal("fetch", vi.fn(async () =>
		new Response(SVG, { status: 200, headers: { "Content-Type": "image/svg+xml" } }),
	));
});

afterEach(() => {
	container.remove();
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

// trace runs the app's real flow: pick a file, press the trace button, let the
// promise chain settle.
async function trace() {
	await act(async () => {
		root.render(<App />);
	});

	const input = container.querySelector<HTMLInputElement>('input[type="file"]');
	expect(input, "the upload zone must expose a file input").not.toBeNull();

	const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "in.png", { type: "image/png" });
	Object.defineProperty(input!, "files", { value: [file], configurable: true });

	await act(async () => {
		input!.dispatchEvent(new Event("change", { bubbles: true }));
	});

	const run = [...container.querySelectorAll("button")].find((button) =>
		/trace/i.test(button.textContent || ""),
	);
	expect(run, "the trace button must be present").toBeDefined();

	await act(async () => {
		run!.click();
	});
	// the fetch chain settles across a few microtask turns
	for (let turn = 0; turn < 5; turn++) {
		await act(async () => {
			await Promise.resolve();
		});
	}
}

describe("the traced SVG's object URL", () => {
	it("is what the result view loads, as an image/svg+xml blob", async () => {
		await trace();

		const image = container.querySelector<HTMLImageElement>(".result-layer img");
		expect(image, "the result must render as a loaded image").not.toBeNull();

		const source = image!.getAttribute("src")!;
		expect(created, "the rendered URL must be one this app minted").toContain(source);
		// ⚠️ The type matters: a blob with no type loads as a download rather than
		// an image, so the preview would be empty with nothing in the console.
		expect(source).toContain("image/svg+xml");

		expect(container.innerHTML, "the traced markup reached this document").not.toContain(MARKER);
		expect(
			container.querySelector(".stage svg"),
			"the output stage must hold an <img>, never a parsed SVG tree",
		).toBeNull();
	});

	it("is released when the app unmounts", async () => {
		await trace();

		const source = container.querySelector<HTMLImageElement>(".result-layer img")!.getAttribute("src")!;
		expect(revoked).not.toContain(source);

		await act(async () => {
			root.unmount();
		});

		expect(revoked, "an un-revoked object URL pins its blob for the life of the tab").toContain(
			source,
		);
	});

	it("is released when a new file replaces the output", async () => {
		await trace();
		const first = container.querySelector<HTMLImageElement>(".result-layer img")!.getAttribute("src")!;

		const replace = [...container.querySelectorAll("button")].find((button) =>
			/replace/i.test(button.textContent || ""),
		);
		expect(replace, "the upload zone must offer a replace action once a file is picked").toBeDefined();

		await act(async () => {
			replace!.click();
		});

		expect(revoked, "resetting the output must release the SVG it was showing").toContain(first);
		expect(container.querySelector(".result-layer img")).toBeNull();

		await act(async () => {
			root.unmount();
		});
	});
});
