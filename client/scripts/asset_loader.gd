class_name AssetLoader
extends Node
## Загрузчик игровых ресурсов с сервера.
## - Манифест: список файлов с размером и SHA-256, группы "start" (до входа) и "bg" (в фоне).
## - Докачка: файл качается кусками (HTTP Range) в `.part`; после обрыва/перезапуска продолжается с места обрыва.
## - Целостность: после загрузки проверяется SHA-256; при несовпадении файл качается заново (до MAX_RETRIES).
## - Обновление: качаются только файлы, чей SHA-256 отличается от локального; лишние локальные файлы удаляются.
## - Место: до загрузки проверяется свободное место (если платформа его сообщает).

signal progress(done_bytes: int, total_bytes: int)

const ROOT := "user://assets/"
const STATE_FILE := "user://assets_state.json"
const CHUNK := 1024 * 1024
const MAX_RETRIES := 4
const SPACE_MARGIN := 1.1

var base_url: String = ""
## Тестовые параметры (читаются из окружения в boot.gd).
var test_abort_after_bytes: int = 0
var verify_full: bool = false

var stats := {"downloaded": 0, "skipped": 0, "bytes": 0, "resumed_bytes": 0, "repaired": 0, "pruned": 0}
var last_error: String = ""

var _state := {"files": {}, "part_for": {}, "manifest_version": ""}
var _downloaded_this_run: int = 0


func _ready() -> void:
	_load_state()


## Возвращает манифест или пустой словарь при ошибке.
func fetch_manifest() -> Dictionary:
	var r := await _http_get("/api/assets/manifest", {})
	if not r.ok or r.code != 200:
		last_error = "manifest_http_%d" % r.code
		return {}
	var parsed: Variant = JSON.parse_string((r.body as PackedByteArray).get_string_from_utf8())
	if parsed is Dictionary and parsed.has("files"):
		return parsed
	last_error = "manifest_bad_json"
	return {}


## Синхронизирует группу файлов. true — все файлы группы на месте и проверены.
func sync_group(manifest: Dictionary, group: String) -> bool:
	var files: Array = []
	var total := 0
	for f in manifest["files"]:
		if str(f["group"]) == group:
			files.append(f)
			total += int(f["size"])
	var done := 0
	var pending: Array = []
	for f in files:
		if _is_present(f):
			done += int(f["size"])
			stats.skipped += 1
		else:
			pending.append(f)
	if not _enough_space(pending):
		last_error = "no_space"
		return false
	progress.emit(done, total)
	for f in pending:
		var before := done
		var ok := await _download(f, func(n: int) -> void: progress.emit(before + n, total))
		if not ok:
			return false
		done += int(f["size"])
		progress.emit(done, total)
	return true


## Удаляет локальные файлы, которых нет в манифесте. Вызывать после синхронизации всех групп.
func prune(manifest: Dictionary) -> void:
	var keep := {}
	for f in manifest["files"]:
		keep[str(f["path"])] = true
	for rel in _list_files(ROOT, ""):
		var base: String = rel.trim_suffix(".part")
		if not keep.has(base):
			DirAccess.remove_absolute(ROOT + rel)
			stats.pruned += 1
	for k in _state["files"].keys():
		if not keep.has(k):
			_state["files"].erase(k)
	for k in _state["part_for"].keys():
		if not keep.has(k):
			_state["part_for"].erase(k)
	_state["manifest_version"] = str(manifest.get("version", ""))
	_save_state()


func summary() -> String:
	return "downloaded=%d skipped=%d bytes=%d resumed_bytes=%d repaired=%d pruned=%d" % [
		stats.downloaded, stats.skipped, stats.bytes, stats.resumed_bytes, stats.repaired, stats.pruned]


# ---------------------------------------------------------------- внутреннее

func _is_present(f: Dictionary) -> bool:
	var rel := str(f["path"])
	var path := ROOT + rel
	var rec: Variant = _state["files"].get(rel)
	if rec == null or str(rec["sha256"]) != str(f["sha256"]) or not FileAccess.file_exists(path):
		return false
	if _file_size(path) != int(f["size"]):
		return false
	if verify_full and _sha256_file(path) != str(f["sha256"]):
		# файл повреждён на диске: удаляем и считаем «починкой»
		DirAccess.remove_absolute(path)
		_state["files"].erase(rel)
		stats.repaired += 1
		return false
	return true


func _enough_space(pending: Array) -> bool:
	var need := 0
	for f in pending:
		var part := ROOT + str(f["path"]) + ".part"
		var have := _file_size(part) if FileAccess.file_exists(part) else 0
		need += max(0, int(f["size"]) - have)
	var d := DirAccess.open("user://")
	if d == null:
		return true
	var free := d.get_space_left()
	if free <= 0:
		return true  # платформа не сообщила — не блокируем (запись всё равно проверяется)
	return float(need) * SPACE_MARGIN <= float(free)


func _download(f: Dictionary, on_bytes: Callable) -> bool:
	var rel := str(f["path"])
	var size := int(f["size"])
	var want := str(f["sha256"])
	var final_path := ROOT + rel
	var part := final_path + ".part"
	DirAccess.make_dir_recursive_absolute(final_path.get_base_dir())
	# Остаток от другой версии файла продолжать нельзя.
	if FileAccess.file_exists(part) and str(_state["part_for"].get(rel, "")) != want:
		DirAccess.remove_absolute(part)
	_state["part_for"][rel] = want
	_save_state()

	if FileAccess.file_exists(part):
		stats.resumed_bytes += _file_size(part)
	var failures := 0
	var hash_failures := 0
	while true:
		var have := _file_size(part) if FileAccess.file_exists(part) else 0
		if have > size:
			DirAccess.remove_absolute(part)
			have = 0
		if have == size:
			if _sha256_file(part) == want:
				DirAccess.rename_absolute(part, final_path)
				_state["files"][rel] = {"sha256": want, "size": size}
				_state["part_for"].erase(rel)
				_save_state()
				stats.downloaded += 1
				return true
			# целостность нарушена — качаем заново
			DirAccess.remove_absolute(part)
			hash_failures += 1
			if hash_failures >= MAX_RETRIES:
				last_error = "hash_mismatch:" + rel
				return false
			continue
		var end := mini(have + CHUNK, size) - 1
		var r := await _http_get("/assets/" + rel, {"Range": "bytes=%d-%d" % [have, end]})
		if not r.ok or (r.code != 206 and r.code != 200):
			failures += 1
			if failures >= MAX_RETRIES:
				last_error = "download_failed:%s:%d" % [rel, r.code]
				return false
			await get_tree().create_timer(0.5 * pow(2, failures - 1)).timeout
			continue
		var body: PackedByteArray = r.body
		if r.code == 200:
			# сервер проигнорировал Range и вернул весь файл
			if body.size() != size:
				failures += 1
				if failures >= MAX_RETRIES:
					last_error = "bad_length:" + rel
					return false
				continue
			DirAccess.remove_absolute(part)
			have = 0
		elif body.size() != end - have + 1:
			failures += 1
			if failures >= MAX_RETRIES:
				last_error = "bad_chunk:" + rel
				return false
			continue
		var fa := FileAccess.open(part, FileAccess.READ_WRITE if FileAccess.file_exists(part) else FileAccess.WRITE)
		if fa == null:
			last_error = "write_failed:" + rel
			return false
		fa.seek(have)
		fa.store_buffer(body)
		fa.close()
		failures = 0
		_downloaded_this_run += body.size()
		stats.bytes += body.size()
		on_bytes.call(have + body.size())
		if test_abort_after_bytes > 0 and _downloaded_this_run >= test_abort_after_bytes:
			print("TEST_ABORT part=", rel, " have=", have + body.size())
			get_tree().quit(3)
			await get_tree().create_timer(5.0).timeout
	return false


func _http_get(path: String, headers: Dictionary) -> Dictionary:
	var req := HTTPRequest.new()
	req.timeout = 20.0
	add_child(req)
	var hs := PackedStringArray()
	for k in headers:
		hs.append("%s: %s" % [k, headers[k]])
	var err := req.request(base_url + path, hs, HTTPClient.METHOD_GET)
	if err != OK:
		req.queue_free()
		return {"ok": false, "code": 0, "body": PackedByteArray()}
	var res: Array = await req.request_completed
	req.queue_free()
	return {"ok": res[0] == HTTPRequest.RESULT_SUCCESS, "code": int(res[1]), "body": res[3]}


func _file_size(path: String) -> int:
	var fa := FileAccess.open(path, FileAccess.READ)
	if fa == null:
		return -1
	var n := fa.get_length()
	fa.close()
	return n


func _sha256_file(path: String) -> String:
	var fa := FileAccess.open(path, FileAccess.READ)
	if fa == null:
		return ""
	var ctx := HashingContext.new()
	ctx.start(HashingContext.HASH_SHA256)
	while not fa.eof_reached():
		var buf := fa.get_buffer(CHUNK)
		if buf.size() > 0:
			ctx.update(buf)
	fa.close()
	return ctx.finish().hex_encode()


func _list_files(root: String, rel: String) -> Array:
	var out: Array = []
	var d := DirAccess.open(root + rel)
	if d == null:
		return out
	for dir_name in d.get_directories():
		out.append_array(_list_files(root, rel + dir_name + "/"))
	for fname in d.get_files():
		out.append(rel + fname)
	return out


func _load_state() -> void:
	if not FileAccess.file_exists(STATE_FILE):
		return
	var parsed: Variant = JSON.parse_string(FileAccess.get_file_as_string(STATE_FILE))
	if parsed is Dictionary and parsed.has("files") and parsed.has("part_for"):
		_state = parsed


func _save_state() -> void:
	var tmp := STATE_FILE + ".tmp"
	var fa := FileAccess.open(tmp, FileAccess.WRITE)
	if fa == null:
		return
	fa.store_string(JSON.stringify(_state))
	fa.close()
	DirAccess.rename_absolute(tmp, STATE_FILE)
