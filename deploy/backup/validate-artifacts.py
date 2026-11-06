#!/usr/bin/env python3
"""Fail-closed validation of backup artifacts; no shell evaluation of recovered data."""
import base64
import hashlib
import pathlib
import re
import sys
import tarfile

UUID = r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"


def checked_file(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"artefato ausente, ilegível ou symlink: {path.name}")
    checksum_path = pathlib.Path(str(path) + ".sha256")
    if checksum_path.is_symlink() or not checksum_path.is_file():
        raise ValueError(f"checksum ausente: {path.name}")
    expected = checksum_path.read_text().strip()
    if not re.fullmatch(r"[0-9a-f]{64}", expected):
        raise ValueError(f"checksum malformado: {path.name}")
    content = path.read_bytes()
    if hashlib.sha256(content).hexdigest() != expected:
        raise ValueError(f"artefato corrompido: {path.name}")
    return content


def extract_ledgers(archive, destination):
    with tarfile.open(archive) as source:
        for member in source:
            parts = pathlib.PurePosixPath(member.name).parts
            if member.isdir() and parts == ("ledgers",):
                continue
            if (not member.isfile() or len(parts) != 2 or parts[0] != "ledgers"
                    or not re.fullmatch(r"(?:attachments|deletions)-[A-Za-z0-9.\-]+\.tsv(?:\.sha256)?", parts[1])
                    or member.size > 512 * 1024 * 1024):
                raise ValueError("arquivo de livros contém caminho, tipo ou arquivo parcial inesperado")
            target = destination / "ledgers" / parts[1]
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with target.open("xb") as output:
                output.write(source.extractfile(member).read())


def verify_ledgers(directory):
    names = set()
    for kind in ("attachments", "deletions"):
        files = sorted(directory.glob(f"{kind}-*.tsv"))
        if not files:
            raise ValueError(f"livro obrigatório ausente: {kind}")
        for path in files:
            content = checked_file(path)
            names.add(path.name)
            names.add(path.name + ".sha256")
            lines = content.decode("utf-8").splitlines()
            if (not content.endswith(b"\n") or len(lines) < 2
                    or lines[0] != f"# chatisp-{kind}-ledger-1"
                    or lines[-1] != f"# end-chatisp-{kind}-ledger-1"):
                raise ValueError(f"livro truncado: {path.name}")
            for row in lines[1:-1]:
                fields = row.split("\t")
                if kind == "deletions":
                    valid = (len(fields) == 3 and re.fullmatch(UUID, fields[0])
                             and fields[1] in ("attachment", "credential_secret", "contact_privacy")
                             and re.fullmatch(UUID, fields[2]))
                else:
                    valid = (len(fields) == 5 and re.fullmatch(UUID, fields[0])
                             and re.fullmatch(UUID, fields[1])
                             and re.fullmatch(f"{UUID}/{UUID}", fields[2])
                             and re.fullmatch(r"[0-9]+", fields[4]))
                if not valid:
                    raise ValueError(f"linha malformada em {path.name}")
    if {p.name for p in directory.iterdir()} != names:
        raise ValueError("livros parciais, checksums órfãos ou arquivos inesperados no destino")
    attachments = {n[len("attachments-"):] for n in names if n.startswith("attachments-")}
    deletions = {n[len("deletions-"):] for n in names if n.startswith("deletions-")}
    if attachments != deletions:
        raise ValueError("exportação de livros incompleta: mapa e exclusões não formam pares")


def verify_keys(path):
    lines = path.read_text().splitlines()
    if len(lines) != 2 or not lines[0].startswith("CHATISP_DATA_KEYS=") or not lines[1].startswith("CHATISP_DATA_KEY_VERSION="):
        raise ValueError("cópia de chaves ilegível ou malformada")
    active = lines[1][len("CHATISP_DATA_KEY_VERSION="):]
    keys = {}
    for entry in lines[0][len("CHATISP_DATA_KEYS="): ].split(","):
        version, encoded = entry.split(":", 1)
        if (not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,31}", version) or version in keys
                or len(base64.b64decode(encoded + "=" * (-len(encoded) % 4), altchars=b"-_", validate=True)) != 32):
            raise ValueError("anel de chaves malformado")
        keys[version] = encoded
    if active not in keys:
        raise ValueError("versão ativa ausente no anel recuperado")


def verify_references(manifest_path, references_path):
    lines = manifest_path.read_text().splitlines()
    if not lines or lines[0] != "# attachments-mirror-1 key<TAB>version<TAB>sha256<TAB>bytes":
        raise ValueError("manifesto de objetos ausente ou malformado")
    if len(lines) < 2:
        raise ValueError("manifesto de objetos truncado")
    rows = lines[1:-1]
    footer = "# end-attachments-mirror-1 entries=%d sha256=%s" % (
        len(rows), hashlib.sha256("".join(row + "\n" for row in rows).encode()).hexdigest())
    if lines[-1] != footer:
        raise ValueError("manifesto de objetos truncado ou corrompido")
    manifest = {}
    for line in rows:
        fields = line.split("\t")
        if len(fields) != 4 or fields[0] in manifest:
            raise ValueError("manifesto de objetos com linhas inválidas ou duplicadas")
        manifest[fields[0]] = fields[1:]
    for row in references_path.read_text().splitlines():
        fields = row.split("\t")
        if len(fields) != 5:
            raise ValueError("referência de anexo inválida no banco restaurado")
        key, version, digest, size, state = fields
        if state == "validated" and not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise ValueError("anexo validado sem hash íntegro no banco restaurado")
        if (key not in manifest or manifest[key][0] != version or manifest[key][2] != size
                or (digest and manifest[key][1] != digest)):
            raise ValueError(f"espelho não cobre anexo sobrevivente do banco: {key}")


def main():
    action = sys.argv[1]
    if action == "extract-ledgers":
        extract_ledgers(sys.argv[2], pathlib.Path(sys.argv[3]))
    elif action == "verify-ledgers":
        verify_ledgers(pathlib.Path(sys.argv[2]))
    elif action == "verify-keys":
        verify_keys(pathlib.Path(sys.argv[2]))
    elif action == "verify-references":
        verify_references(pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3]))
    else:
        raise ValueError("operação desconhecida")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, tarfile.TarError) as error:
        sys.exit(f"backup inválido: {error}")
