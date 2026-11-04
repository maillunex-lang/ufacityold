extends Control
## Экран запуска: проверка сервера, гостевой вход. Этап 1: технический прототип (не финальный экран).

const DEFAULT_BASE_URL := "https://tts-ufa.ru/LunexGame"
const SESSION_FILE := "user://session.cfg"

var base_url: String = DEFAULT_BASE_URL
var smoke: bool = false
var status_label: Label
var info_label: Label
var retry_button: Button
var progress_bar: ProgressBar
var loader: AssetLoader
var battle_button: Button


func _ready() -> void:
	var override := OS.get_environment("LUNEXGAME_BASE_URL")
	if override != "":
		base_url = override
	smoke = OS.get_environment("LUNEXGAME_SMOKE") == "1"
	_build_ui()
	_run()


func _build_ui() -> void:
	var bg := ColorRect.new()
	bg.color = Color("06251f")
	bg.set_anchors_preset(Control.PRESET_FULL_RECT)
	add_child(bg)
	var box := VBoxContainer.new()
	box.set_anchors_preset(Control.PRESET_FULL_RECT)
	box.alignment = BoxContainer.ALIGNMENT_CENTER
	box.add_theme_constant_override("separation", 32)
	add_child(box)
	var title := _label("Самрая:\nПробуждение легенд", 72, Color("d4af37"))
	box.add_child(title)
	status_label = _label("", 44, Color("e8d9a0"))
	box.add_child(status_label)
	info_label = _label("", 32, Color("3fe0c5"))
	box.add_child(info_label)
	progress_bar = ProgressBar.new()
	progress_bar.custom_minimum_size = Vector2(700, 36)
	progress_bar.show_percentage = false
	progress_bar.size_flags_horizontal = Control.SIZE_SHRINK_CENTER
	progress_bar.hide()
	box.add_child(progress_bar)
	battle_button = Button.new()
	battle_button.text = "В бой"
	battle_button.custom_minimum_size = Vector2(500, 140)
	battle_button.add_theme_font_size_override("font_size", 56)
	battle_button.size_flags_horizontal = Control.SIZE_SHRINK_CENTER
	battle_button.pressed.connect(func() -> void: get_tree().change_scene_to_file("res://scenes/battle.tscn"))
	battle_button.hide()
	box.add_child(battle_button)
	retry_button = Button.new()
	retry_button.text = "Повторить"
	retry_button.custom_minimum_size = Vector2(400, 120)
	retry_button.add_theme_font_size_override("font_size", 44)
	retry_button.size_flags_horizontal = Control.SIZE_SHRINK_CENTER
	retry_button.pressed.connect(_run)
	retry_button.hide()
	box.add_child(retry_button)


func _label(text: String, size: int, color: Color) -> Label:
	var l := Label.new()
	l.text = text
	l.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	l.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	l.add_theme_font_size_override("font_size", size)
	l.add_theme_color_override("font_color", color)
	return l


func _run() -> void:
	retry_button.hide()
	battle_button.hide()
	status_label.text = "Подключение к серверу…"
	var v: Dictionary = await _request("/api/version", HTTPClient.METHOD_GET, "")
	if v.is_empty():
		_fail("Сервер недоступен")
		return
	var ver := str(v.get("version", "?"))
	var short_commit := str(v.get("commit", "?")).substr(0, 7)
	info_label.text = "Сервер %s" % ver if ver == short_commit else "Сервер %s (%s)" % [ver, short_commit]

	var token := _load_token()
	var me: Dictionary = {}
	if token != "":
		me = await _request("/api/me", HTTPClient.METHOD_GET, token)
	if me.is_empty():
		var g: Dictionary = await _request("/api/auth/guest", HTTPClient.METHOD_POST, "")
		if not g.has("token"):
			_fail("Не удалось войти гостем")
			return
		token = str(g["token"])
		_save_token(token)
		me = await _request("/api/me", HTTPClient.METHOD_GET, token)
		if me.is_empty():
			_fail("Не удалось проверить вход")
			return
	var account_id := int(me["accountId"])
	status_label.text = "Вы вошли как гость #%d" % account_id
	print("BOOT_OK account=", account_id)

	Session.base_url = base_url
	Session.token = token
	Session.account_id = account_id
	if not await _sync_assets():
		return
	if smoke:
		if OS.get_environment("LUNEXGAME_SMOKE_BATTLE") == "1":
			get_tree().change_scene_to_file("res://scenes/battle.tscn")
		else:
			get_tree().quit(0)
		return
	battle_button.show()


## Стартовый набор блокирует вход, остальное докачивается в фоне (пока без игрового экрана).
func _sync_assets() -> bool:
	if loader != null:
		loader.queue_free()
	loader = AssetLoader.new()
	loader.base_url = base_url
	loader.test_abort_after_bytes = int(OS.get_environment("LUNEXGAME_TEST_ABORT_AFTER_BYTES")) if OS.get_environment("LUNEXGAME_TEST_ABORT_AFTER_BYTES") != "" else 0
	loader.verify_full = OS.get_environment("LUNEXGAME_VERIFY_FULL") == "1"
	add_child(loader)
	loader.progress.connect(_on_progress)
	var manifest: Dictionary = await loader.fetch_manifest()
	if manifest.is_empty():
		_fail("Не удалось получить список ресурсов (%s)" % loader.last_error)
		return false
	progress_bar.show()
	status_label.text = "Загрузка стартовых ресурсов…"
	if not await loader.sync_group(manifest, "start"):
		_fail("Ошибка загрузки ресурсов: " + loader.last_error)
		return false
	print("ASSETS_START_OK ", loader.summary())
	status_label.text = "Фоновая загрузка ресурсов…"
	for g in _other_groups(manifest):
		if not await loader.sync_group(manifest, g):
			_fail("Ошибка загрузки ресурсов: " + loader.last_error)
			return false
	loader.prune(manifest)
	progress_bar.hide()
	status_label.text = "Ресурсы загружены"
	print("ASSETS_OK ", loader.summary())
	return true


func _other_groups(manifest: Dictionary) -> Array:
	var gs: Array = []
	for f in manifest["files"]:
		var g := str(f["group"])
		if g != "start" and not gs.has(g):
			gs.append(g)
	gs.sort()
	return gs


func _on_progress(done: int, total: int) -> void:
	progress_bar.max_value = max(total, 1)
	progress_bar.value = done
	info_label.text = "%.1f / %.1f МБ" % [done / 1048576.0, total / 1048576.0]


func _fail(msg: String) -> void:
	status_label.text = msg
	retry_button.show()
	push_error("BOOT_FAIL: " + msg)
	if smoke:
		get_tree().quit(1)


## Возвращает разобранный JSON-объект; пустой словарь при любой ошибке (сеть, не 200, не JSON).
func _request(path: String, method: int, token: String) -> Dictionary:
	var req := HTTPRequest.new()
	req.timeout = 10.0
	add_child(req)
	var headers := PackedStringArray()
	if token != "":
		headers.append("Authorization: Bearer " + token)
	var err := req.request(base_url + path, headers, method, "")
	if err != OK:
		req.queue_free()
		return {}
	var res: Array = await req.request_completed
	req.queue_free()
	if res[0] != HTTPRequest.RESULT_SUCCESS or res[1] != 200:
		return {}
	var parsed: Variant = JSON.parse_string((res[3] as PackedByteArray).get_string_from_utf8())
	if parsed is Dictionary:
		return parsed
	return {}


func _load_token() -> String:
	var cfg := ConfigFile.new()
	if cfg.load(SESSION_FILE) != OK:
		return ""
	return str(cfg.get_value("session", "token", ""))


func _save_token(token: String) -> void:
	var cfg := ConfigFile.new()
	cfg.set_value("session", "token", token)
	cfg.save(SESSION_FILE)
