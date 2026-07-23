// Inline SVG icons ported from the approved mock. Sizing is handled by parent
// CSS (`.btn svg`, `.lock svg`, `.drop .cloud`, etc.), so these render at
// whatever the context dictates.

type IconProps = { className?: string };

const stroke = {
	fill: "none",
	stroke: "currentColor",
	strokeLinecap: "round" as const,
	strokeLinejoin: "round" as const,
};

export const LogoMark = () => (
	<svg viewBox="0 0 24 24" fill="none">
		<rect x="2" y="2" width="6" height="6" rx="1.2" fill="#46c9ac" />
		<rect x="9.5" y="2" width="6" height="6" rx="1.2" fill="#46c9ac" opacity=".5" />
		<rect x="2" y="9.5" width="6" height="6" rx="1.2" fill="#46c9ac" opacity=".5" />
		<path d="M16 15 L21 20 M21 15 L16 20" stroke="#5fe0c1" strokeWidth="1.6" strokeLinecap="round" />
		<circle cx="16" cy="15" r="1.7" fill="#5fe0c1" />
		<circle cx="21" cy="20" r="1.7" fill="#5fe0c1" />
		<circle cx="21" cy="15" r="1.7" fill="#5fe0c1" />
		<circle cx="16" cy="20" r="1.7" fill="#5fe0c1" />
	</svg>
);

export const CloudIcon = ({ className }: IconProps) => (
	<svg className={className} viewBox="0 0 24 24" strokeWidth="1.6" {...stroke}>
		<path d="M12 13v8" />
		<path d="m8 17 4-4 4 4" />
		<path d="M20 16.5A4.5 4.5 0 0 0 17 8.2 6 6 0 0 0 5.2 9 4 4 0 0 0 5 17" />
	</svg>
);

export const ReplaceIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<path d="M3 12a9 9 0 0 1 15-6.7L21 8M21 3v5h-5M21 12a9 9 0 0 1-15 6.7L3 16M3 21v-5h5" />
	</svg>
);

export const LockIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<rect x="5" y="11" width="14" height="10" rx="2" />
		<path d="M8 11V7a4 4 0 0 1 8 0v4" />
	</svg>
);

export const PlayIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2" {...stroke}>
		<path d="M5 3l14 9-14 9V3z" />
	</svg>
);

export const CompareIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<path d="M12 3v18" />
		<path d="M5 7l-2 2 2 2" />
		<path d="M19 7l2 2-2 2" />
	</svg>
);

export const ExpandIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<path d="M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7" />
	</svg>
);

export const ImageIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.4" {...stroke}>
		<rect x="3" y="3" width="18" height="18" rx="2" />
		<circle cx="9" cy="9" r="2" />
		<path d="m21 15-5-5L5 21" />
	</svg>
);

export const AlertIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2" {...stroke}>
		<path d="M12 9v4m0 4h.01" />
		<path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
	</svg>
);

export const RetryIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<path d="M3 12a9 9 0 0 1 15-6.7L21 8M21 3v5h-5" />
	</svg>
);

export const CompareHandleIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2.4" {...stroke}>
		<path d="M9 7l-5 5 5 5M15 7l5 5-5 5" />
	</svg>
);

export const CopyIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<rect x="9" y="9" width="12" height="12" rx="2" />
		<path d="M5 15V5a2 2 0 0 1 2-2h10" />
	</svg>
);

export const CheckIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2" {...stroke}>
		<path d="M20 6 9 17l-5-5" />
	</svg>
);

export const DownloadIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<path d="M12 3v12m0 0 4-4m-4 4-4-4M4 19h16" />
	</svg>
);

export const TrashIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="1.8" {...stroke}>
		<path d="M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14" />
	</svg>
);

export const MinusIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2" {...stroke}>
		<path d="M5 12h14" />
	</svg>
);

export const PlusIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2" {...stroke}>
		<path d="M12 5v14M5 12h14" />
	</svg>
);

export const FitIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2" {...stroke}>
		<path d="M3 8V4h4M21 8V4h-4M3 16v4h4M21 16v4h-4" />
	</svg>
);

export const CloseIcon = () => (
	<svg viewBox="0 0 24 24" strokeWidth="2" {...stroke}>
		<path d="M18 6 6 18M6 6l12 12" />
	</svg>
);
