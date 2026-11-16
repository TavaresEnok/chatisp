#!/usr/bin/env python3
"""Generate documentation/source bundles from explicit allowlists, never runtime data."""
from pathlib import Path
import hashlib
import json
import zipfile

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "backend/package-assets"
INVENTORY = ROOT / "docs/design/inventario-funcional-20261107"
BRIEFING = ROOT / "docs/design/briefing-tecnico-frontend"


def files_under(directory, extensions):
    return sorted(p for p in directory.rglob("*") if p.is_file() and not p.is_symlink() and p.suffix in extensions)


def write_zip(filename, files):
    target = OUT / filename
    with zipfile.ZipFile(target, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in sorted(set(files)):
            if path.is_symlink() or not path.is_file():
                raise ValueError("Expected regular source file: " + str(path.relative_to(ROOT)))
            relative = path.relative_to(ROOT).as_posix()
            if any(part in {".env", "node_modules", ".git", ".tmp"} for part in path.relative_to(ROOT).parts):
                raise ValueError("Disallowed package input")
            info = zipfile.ZipInfo(relative, date_time=(2026, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            archive.writestr(info, path.read_bytes())
    with zipfile.ZipFile(target) as archive:
        if archive.testzip() is not None:
            raise ValueError("Invalid ZIP")
    return target


def main():
    OUT.mkdir(exist_ok=True)
    inventory = files_under(INVENTORY, {".md", ".csv", ".json"})
    briefing = [BRIEFING / "CHATISP_STACK_E_BRIEFING_FRONTEND.md"]
    manifests = [ROOT / name for name in [
        "web/package.json", "web/package-lock.json", "web/components.json",
        "web/vite.config.ts", "web/eslint.config.js", "web/tsconfig.json",
        "web/tsconfig.app.json", "web/tsconfig.node.json", "web/index.html",
        "backend/go.mod", "backend/sqlc.yaml", "backend/api/openapi.json",
        "backend/api/public-openapi.json",
    ]]
    source = files_under(ROOT / "web/src", {".ts", ".tsx", ".css"})
    visual = []
    for name in ["redesign-20261106", "redesign-20261107", "demonstracao-20261107", "redesign-20261115"]:
        visual.extend(files_under(ROOT / "docs/design" / name, {".png", ".md"}))
    specs = [
        ("inventario-funcional", "Inventário funcional completo", "Páginas, módulos, checklist de 142 itens e matriz dos 257 requisitos.", "CHATISP_PACOTE_FUNCIONAL_PARA_DESIGN.zip", inventory),
        ("briefing-tecnico", "Briefing técnico e código do frontend", "Stack, fontes React/TypeScript, manifests e contratos OpenAPI para outra IA trabalhar no frontend.", "CHATISP_BRIEFING_TECNICO_E_FRONTEND.zip", inventory + briefing + manifests + source),
        ("referencias-visuais", "Referências e propostas visuais", "Capturas demonstrativas, propostas e documentação visual; conceitos não representam recursos novos já implementados.", "CHATISP_REFERENCIAS_VISUAIS.zip", visual),
    ]
    packages = []
    for package_id, title, description, filename, files in specs:
        path = write_zip(filename, files)
        packages.append(dict(id=package_id, title=title, description=description,
                             filename=filename, size=path.stat().st_size,
                             sha256=hashlib.sha256(path.read_bytes()).hexdigest()))
    (OUT / "manifest.json").write_text(json.dumps({"packages": packages}, ensure_ascii=False, indent=2) + "\n")
    public = ROOT / "web/public/pacotes"
    public.mkdir(parents=True, exist_ok=True)
    bundle = public / "CHATISP_TODOS_PACOTES.zip"
    with zipfile.ZipFile(bundle, "w", zipfile.ZIP_STORED) as archive:
        for path in [OUT / item["filename"] for item in packages] + [OUT / "manifest.json"]:
            info = zipfile.ZipInfo(path.name, date_time=(2026, 1, 1, 0, 0, 0))
            info.external_attr = 0o100644 << 16
            archive.writestr(info, path.read_bytes())
    print("Generated 3 packages, integrity manifest and public /download bundle")


if __name__ == "__main__":
    main()
