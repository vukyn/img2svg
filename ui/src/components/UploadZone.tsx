import { useRef, useState } from "react";

import type { PickedFile } from "../types";
import { human } from "../lib/format";
import { CloudIcon, ReplaceIcon } from "./Icons";

interface UploadZoneProps {
	picked: PickedFile | null;
	onFile: (file: File) => void;
	onReplace: () => void;
}

// Drag-drop + click-to-browse upload zone. When a file is picked it flips to a
// preview card (thumbnail + name/dims/size + Replace). Clipboard paste (⌘V) is
// handled globally in App.
export function UploadZone({ picked, onFile, onReplace }: UploadZoneProps) {
	const inputRef = useRef<HTMLInputElement>(null);
	const [hot, setHot] = useState(false);

	if (picked) {
		const { file, meta, thumbUrl } = picked;
		const sub = meta
			? `${(file.type || "image").replace("image/", "").toUpperCase()} · ${meta.w}×${meta.h}px · ${human(file.size)}`
			: `${file.name.split(".").pop()?.toUpperCase() ?? "IMG"} · ${human(file.size)}`;
		return (
			<div className="drop preview">
				<img className="thumb checker" src={thumbUrl} alt="source preview" />
				<div>
					<div className="meta-name">{file.name}</div>
					<div className="meta-sub">{sub}</div>
					<button type="button" className="btn ghost sm swap" onClick={onReplace}>
						<ReplaceIcon />
						Replace
					</button>
				</div>
			</div>
		);
	}

	const pick = (f: File | undefined | null) => {
		if (f) onFile(f);
	};

	return (
		<div
			className={`drop${hot ? " hot" : ""}`}
			onClick={() => inputRef.current?.click()}
			onDragOver={(e) => {
				e.preventDefault();
				setHot(true);
			}}
			onDragEnter={(e) => {
				e.preventDefault();
				setHot(true);
			}}
			onDragLeave={(e) => {
				e.preventDefault();
				setHot(false);
			}}
			onDrop={(e) => {
				e.preventDefault();
				setHot(false);
				pick(e.dataTransfer.files[0]);
			}}
		>
			<CloudIcon className="cloud" />
			<div className="big">Drop an image, or click to browse</div>
			<div className="paste-hint">…or paste from clipboard</div>
			<div className="formats">
				<span>png</span>
				<span>jpg</span>
				<span>gif</span>
				<span>bmp</span>
				<span>webp</span>
			</div>
			<input
				ref={inputRef}
				type="file"
				accept="image/*"
				hidden
				onChange={(e) => pick(e.target.files?.[0])}
			/>
		</div>
	);
}
