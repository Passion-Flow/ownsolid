def materialize_git_archive_basis(root, manifest, transport_binding, owner_uid, deadline, now=time.monotonic, basis_binding_sha256='0c6cc92715ef72a1d433250c8dbd2c4e8c7890f7314cdbfc636fa286c00dfe96'):
    """Convert one exact Git tar mode mapping into a new private verified cache."""
    old = Path(transport_binding['basis_path'])
    binding_path = old.parent / 'installed-source-verified.json'
    binding_fd = os.open(binding_path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        identity = os.fstat(binding_fd)
        assert stat.S_ISREG(identity.st_mode) and identity.st_uid == owner_uid and identity.st_nlink == 1 and identity.st_size <= 100000
        binding_bytes = os.read(binding_fd, 100001)
    finally:
        os.close(binding_fd)
    assert hashlib.sha256(binding_bytes).hexdigest() == basis_binding_sha256, 'BASIS_OWNER_BINDING_CHANGED'
    binding = json.loads(binding_bytes)
    assert binding['source'] == transport_binding['basis_source'] and binding['tree'] == transport_binding['basis_tree']
    assert binding['paths'] == 5015 and binding['every_source_byte_and_mode_verified'] is True
    changed = set(transport_binding['delta_paths'])
    basis = {ref['path']:ref for ref in manifest['files'] if ref['path'] not in changed}
    for ref in transport_binding['basis_changed_or_removed_inputs']:
        assert ref['path'] not in basis
        basis[ref['path']] = ref
    assert len(basis) == 5015
    destination = root / 'basis-cache'
    destination.mkdir(mode=0o700)
    counts = {}
    total = 0
    for name, reference in sorted(basis.items()):
        assert now() < deadline, 'BASIS_MATERIALIZATION_ADMISSION_DEADLINE_EXCEEDED'
        checked_relative(name)
        assert reference['mode'] in {'100644', '100755'}
        source_mode = int(reference['mode'], 8) & 0o777
        archive_mode = source_mode | 0o020
        # The transport reference has the exact observed tar mode, not a looser
        # acceptable-mode set. Content and Git blob identities remain identical.
        archive_reference = dict(reference, mode=oct(stat.S_IFREG | archive_mode))
        data = read_cached_file(old, name, archive_reference, owner_uid)
        target = destination / name
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
        try:
            offset = 0
            while offset < len(data):
                amount = os.write(fd, data[offset:])
                assert amount > 0
                offset += amount
            os.fchmod(fd, source_mode)
        finally:
            os.close(fd)
        # Check actual newly created file metadata; expected bits are never
        # substituted for observed mode in the source verifier.
        assert read_cached_file(destination, name, reference, owner_uid) == data
        counts[oct(archive_mode) + '->' + oct(source_mode)] = counts.get(oct(archive_mode) + '->' + oct(source_mode), 0) + 1
        total += len(data)
    assert {p.relative_to(destination).as_posix() for p in destination.rglob('*') if p.is_file()} == basis.keys()
    return destination, {'paths':len(basis), 'bytes':total, 'exact_permission_mapping_counts':counts,
                         'old_cache_read_only':True, 'new_cache_owned':True,
                         'strict_source_verifier_unchanged':True,
                         'owner_binding_sha256':hashlib.sha256(binding_bytes).hexdigest()}
