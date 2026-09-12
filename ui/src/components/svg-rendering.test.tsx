import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { CompareSlider } from "./CompareSlider";
import { Lightbox } from "./Lightbox";
import { OutputPanel } from "./OutputPanel";

// The traced SVG arrives from a subprocess, so every view has to treat it as
// untrusted markup. These tests assert the property that makes that safe: the
// bytes are LOADED as a document through <img src>, never injected into this one.
//
// The payload below is not a theoretical shape. SVG injected via innerHTML cannot
// run a <script>, which is what makes the risk easy to talk yourself out of — but
// it does run animation and event handlers, and <animate onbegin> and <image
// onerror> are the two that need no user interaction at all.
const HOSTILE_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">
	<animate onbegin="globalThis.__pwned = 'animate'" attributeName="x" dur="1s" />
	<image href="x" onerror="globalThis.__pwned = 'image'" />
	<path d="M0 0h10v10H0z" fill="#f00" />
</svg>`;

// jsdom implements neither object URLs nor SVG animation, so createObjectURL is
// stubbed to a value shaped like the real thing. That is the whole contract these
// components have with it: they receive a URL string and put it in a src.
const BLOB_URL = "blob:http://localhost/traced-svg";

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
	(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
	container = document.createElement("div");
	document.body.appendChild(container);
	root = createRoot(container);
});

afterEach(() => {
	act(() => root.unmount());
	container.remove();
	delete (globalThis as { __pwned?: string }).__pwned;
});

function render(node: React.ReactNode) {
	act(() => root.render(node));
}

// assertSandboxed is the shared assertion, and it is deliberately three checks
// rather than one. "Renders an <img>" alone would pass a component that ALSO
// injected the markup; "no handler attribute" alone would pass one that rendered
// nothing at all.
function assertSandboxed(where: string) {
	const image = container.querySelector<HTMLImageElement>(`${where} img`);
	expect(image, "the traced SVG must be rendered through an <img>").not.toBeNull();
	expect(image!.getAttribute("src")).toBe(BLOB_URL);

	expect(
		container.querySelector("animate"),
		"the SVG was parsed into this document: its elements are live nodes here",
	).toBeNull();
	expect(container.innerHTML).not.toContain("onbegin");
	expect(container.innerHTML).not.toContain("__pwned");
	expect((globalThis as { __pwned?: string }).__pwned).toBeUndefined();
}

const metrics = {
	name: "trace.svg",
	inBytes: 100,
	outBytes: 200,
	paths: 1,
	viewBox: "0 0 10 10",
	ratio: 0.5,
	ms: 12,
	usedW: 10,
	usedH: 10,
	quality: "faithful" as const,
};

const noop = () => {};

describe("the traced SVG is never injected into the app's DOM", () => {
	it("OutputPanel renders the result layer as a loaded image", () => {
		render(
			<OutputPanel
				state="result"
				compareMode={false}
				svgUrl={BLOB_URL}
				rasterUrl={null}
				metrics={metrics}
				errorCode={null}
				quality="faithful"
				onToggleCompare={noop}
				onExpand={noop}
				onCopy={noop}
				onDownload={noop}
				onRetry={noop}
			/>,
		);
		assertSandboxed(".result-layer");
	});

	it("CompareSlider renders the vector layer as a loaded image", () => {
		render(<CompareSlider rasterUrl="blob:http://localhost/raster" svgUrl={BLOB_URL} />);
		assertSandboxed(".layer.vector");

		// The clip is what the compare view is FOR, and it has to survive the
		// change: it lives on the wrapper, which is why an <img> works here at all.
		const vector = container.querySelector<HTMLDivElement>(".layer.vector");
		expect(vector!.style.clipPath).toContain("inset(");
	});

	it("Lightbox renders the zoom canvas as a loaded image", () => {
		render(<Lightbox svgUrl={BLOB_URL} onClose={noop} />);
		assertSandboxed(".lb-canvas");

		// Zoom is a transform on the wrapper, not anything inside the SVG — the
		// other reason an <img> costs this view nothing.
		const canvas = container.querySelector<HTMLDivElement>(".lb-canvas");
		expect(canvas!.style.transform).toContain("scale(");
	});

	it("no component reaches for dangerouslySetInnerHTML", () => {
		// The three assertions above describe what the components DO. This one
		// describes what they may not do, and it is the one that survives a
		// refactor adding a fourth view: a source-level ban, so reintroducing the
		// pattern anywhere under components/ fails here rather than waiting for
		// somebody to write the matching render test.
		//
		// The test file itself is excluded for the obvious reason — this very line
		// names the thing it bans.
		const sources = import.meta.glob("./*.tsx", {
			query: "?raw",
			import: "default",
			eager: true,
		}) as Record<string, string>;

		const components = Object.entries(sources).filter(([path]) => !path.endsWith(".test.tsx"));
		expect(components.length).toBeGreaterThan(3);
		for (const [path, source] of components) {
			expect(source, `${path} injects markup instead of loading it`).not.toContain(
				"dangerously" + "SetInnerHTML",
			);
		}
	});
});

describe("the hostile fixture is a real one", () => {
	// ⚠️ Without this, every test above would still pass against a payload that was
	// inert to begin with — proving only that nothing happens when nothing could.
	// Injecting the same bytes the way the components used to shows the handler
	// attributes do survive the parse and land in the live DOM.
	it("injected as markup, the payload's handlers reach the DOM", () => {
		const victim = document.createElement("div");
		victim.innerHTML = HOSTILE_SVG;
		document.body.appendChild(victim);

		expect(victim.querySelector("animate")).not.toBeNull();
		expect(victim.querySelector("animate")!.getAttribute("onbegin")).toContain("__pwned");
		expect(victim.querySelector("image")!.getAttribute("onerror")).toContain("__pwned");

		victim.remove();
	});
});
