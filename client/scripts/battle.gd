extends Node3D
## Живой бой: картинка идёт только из WebSocket /api/battle/live.
## Сохранённая HTTP-запись сверяется в конце, но не проигрывается.
## Модели — ВРЕМЕННЫЕ примитивы.

const ROLE_COLORS := {
	"tank": Color("5b7fa6"), "fighter": Color("c0504d"), "archer": Color("6aa84f"),
	"mage": Color("8e6bbf"), "healer": Color("d4af37"),
}
const FRONT_X := 1.6
const BACK_X := 3.5
const FRONT_Z := [-1.6, 1.6]
const BACK_Z := [-3.2, 0.0, 3.2]
const SMOKE_SPEED := 40.0

var speed: float = 1.0
var smoke: bool = false
var events: Array = []
var result: Dictionary = {}
var battle_id: String = ""
var units: Dictionary = {}  # id -> Dictionary
var t_ms: float = 0.0
var idx: int = 0
var playing: bool = false
var finished: bool = false
var inconsistencies: int = 0
var acted_dead: Dictionary = {}
var final_positions: Dictionary = {}
var movement_events: int = 0
var auto_skill_events: Array[int] = [0, 0]
var passive_events: int = 0

var timer_label: Label
var result_label: Label
var status_label: Label
var again_button: Button
var portraits: Dictionary = {}  # id -> {bar, tint}
var portrait_box: HBoxContainer
var hud: MarginContainer
var speed_buttons: Dictionary = {}
var allowed_speeds: Array = [1]
var selected_speed: int = 1
var auto_on: bool = true
var auto_button: Button
var live_ws: WebSocketPeer
var spawned: bool = false
var live_done: bool = false
var failed: bool = false
var server_t: int = 0
var connect_msec: int = 0
var reconnects: int = 0
var saw_tick: bool = false
var smoke_dropped: bool = false
var resume_seen: bool = false
var command_sent: bool = false
var command_reported: bool = false
var connected_reported: bool = false
var tick_reported: bool = false
var start_sent: bool = false


func _ready() -> void:
	smoke = OS.get_environment("LUNEXGAME_SMOKE") == "1"
	if smoke:
		speed = SMOKE_SPEED
	if OS.get_environment("LUNEXGAME_BATTLE_SPEED") != "":
		speed = float(OS.get_environment("LUNEXGAME_BATTLE_SPEED"))  # для записи кадров/отладки
	_build_world()
	_build_ui()
	_connect_live()


func _exit_tree() -> void:
	if live_ws != null:
		live_ws.close()
		live_ws = null


func _build_world() -> void:
	var env := WorldEnvironment.new()
	var e := Environment.new()
	e.background_mode = Environment.BG_COLOR
	e.background_color = Color("06251f")
	e.ambient_light_source = Environment.AMBIENT_SOURCE_COLOR
	e.ambient_light_color = Color("7fb5a8")
	e.ambient_light_energy = 0.8
	env.environment = e
	add_child(env)
	var sun := DirectionalLight3D.new()
	sun.rotation_degrees = Vector3(-50, -30, 0)
	sun.light_energy = 1.0
	add_child(sun)
	var floor_mesh := MeshInstance3D.new()
	var plane := PlaneMesh.new()
	plane.size = Vector2(18, 12)
	floor_mesh.mesh = plane
	var fm := StandardMaterial3D.new()
	fm.albedo_color = Color("0d3b31")
	floor_mesh.material_override = fm
	add_child(floor_mesh)
	var cam := Camera3D.new()
	cam.keep_aspect = Camera3D.KEEP_WIDTH  # портретный экран: ширина поля боя задаёт обзор
	cam.fov = 62
	cam.position = Vector3(0, 7.2, 10.2)  # сбоку и сверху (угол ~35°)
	add_child(cam)
	cam.look_at(Vector3(0, 0.3, 0.4))


func _build_ui() -> void:
	var layer := CanvasLayer.new()
	add_child(layer)
	# Всё HUD — в контейнере с отступами безопасной зоны (вырез камеры) и общими полями.
	hud = MarginContainer.new()
	hud.set_anchors_preset(Control.PRESET_FULL_RECT)
	hud.mouse_filter = Control.MOUSE_FILTER_IGNORE
	layer.add_child(hud)
	var col := VBoxContainer.new()
	col.mouse_filter = Control.MOUSE_FILTER_IGNORE
	hud.add_child(col)
	var top := VBoxContainer.new()
	top.mouse_filter = Control.MOUSE_FILTER_IGNORE
	col.add_child(top)
	timer_label = _ui_label("0:00 / 1:30", 48, Color("e8d9a0"))
	top.add_child(timer_label)
	top.add_child(_ui_label("ВРЕМЕННЫЕ МОДЕЛИ И БАЛАНС (прототип)", 28, Color("3fe0c5")))
	var gap := Control.new()
	gap.custom_minimum_size = Vector2(0, 60)
	top.add_child(gap)
	# Статусы и итог — в пустой области под таймером, над полем боя.
	var msg := VBoxContainer.new()
	msg.mouse_filter = Control.MOUSE_FILTER_IGNORE
	msg.add_theme_constant_override("separation", 24)
	top.add_child(msg)
	status_label = _ui_label("Загрузка боя…", 40, Color("e8d9a0"))
	msg.add_child(status_label)
	result_label = _ui_label("", 96, Color("d4af37"))
	result_label.hide()
	msg.add_child(result_label)
	again_button = Button.new()
	again_button.text = "Ещё бой"
	again_button.custom_minimum_size = Vector2(420, 120)
	again_button.add_theme_font_size_override("font_size", 48)
	again_button.size_flags_horizontal = Control.SIZE_SHRINK_CENTER
	again_button.pressed.connect(_again)
	again_button.hide()
	msg.add_child(again_button)
	var speed_row := HBoxContainer.new()
	speed_row.alignment = BoxContainer.ALIGNMENT_CENTER
	speed_row.add_theme_constant_override("separation", 12)
	for value in [1, 2, 4]:
		var button := Button.new()
		button.text = "x%d" % value
		button.custom_minimum_size = Vector2(150, 80)
		button.add_theme_font_size_override("font_size", 34)
		_style_speed_button(button, value, false, false)
		button.pressed.connect(func() -> void: _set_battle_speed(value))
		speed_row.add_child(button)
		speed_buttons[value] = button
	msg.add_child(speed_row)
	auto_button = Button.new()
	auto_button.custom_minimum_size = Vector2(280, 80)
	auto_button.add_theme_font_size_override("font_size", 32)
	auto_button.size_flags_horizontal = Control.SIZE_SHRINK_CENTER
	auto_button.pressed.connect(_toggle_auto)
	_style_auto_button()
	msg.add_child(auto_button)
	msg.add_child(_ui_label("x2 после обучения, x4 с VIP 2", 22, Color("a8c8bd")))
	var spacer := Control.new()
	spacer.size_flags_vertical = Control.SIZE_EXPAND_FILL
	spacer.mouse_filter = Control.MOUSE_FILTER_IGNORE
	col.add_child(spacer)
	# Портреты внизу.
	var bar := HBoxContainer.new()
	bar.custom_minimum_size = Vector2(0, 290)
	bar.add_theme_constant_override("separation", 12)
	col.add_child(bar)
	portrait_box = bar
	_apply_safe_area()
	get_viewport().size_changed.connect(_apply_safe_area)


func _apply_safe_area() -> void:
	var ins := SafeArea.insets(get_viewport())
	hud.add_theme_constant_override("margin_left", int(ins["left"]) + 20)
	hud.add_theme_constant_override("margin_right", int(ins["right"]) + 20)
	hud.add_theme_constant_override("margin_top", int(ins["top"]) + 30)
	hud.add_theme_constant_override("margin_bottom", int(ins["bottom"]) + 40)


func _ui_label(text: String, size: int, color: Color) -> Label:
	var l := Label.new()
	l.text = text
	l.add_theme_font_size_override("font_size", size)
	l.add_theme_color_override("font_color", color)
	l.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	l.autowrap_mode = TextServer.AUTOWRAP_WORD_SMART
	l.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	return l


func _make_portrait(def: Dictionary) -> void:
	var box := VBoxContainer.new()
	box.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	var tint := ColorRect.new()
	tint.color = ROLE_COLORS.get(str(def["role"]), Color.GRAY)
	tint.custom_minimum_size = Vector2(0, 150)
	box.add_child(tint)
	var name_l := _ui_label(str(def["name"]), 26, Color.WHITE)
	box.add_child(name_l)
	var en := ProgressBar.new()
	en.max_value = 100
	en.value = 0
	en.show_percentage = false
	en.custom_minimum_size = Vector2(0, 28)
	var sb_fill := StyleBoxFlat.new()
	sb_fill.bg_color = Color("3fe0c5")
	var sb_bg := StyleBoxFlat.new()
	sb_bg.bg_color = Color("0b2a24")
	en.add_theme_stylebox_override("fill", sb_fill)
	en.add_theme_stylebox_override("background", sb_bg)
	box.add_child(en)
	var skill := Button.new()
	skill.text = "Главная"
	skill.custom_minimum_size = Vector2(0, 64)
	skill.add_theme_font_size_override("font_size", 22)
	skill.pressed.connect(func() -> void: _request_main_skill(str(def["id"])))
	box.add_child(skill)
	portrait_box.add_child(box)
	portraits[str(def["id"])] = {"bar": en, "tint": tint}


func _connect_live() -> void:
	if live_ws != null:
		live_ws.close()
		live_ws = null
	start_sent = false
	connected_reported = false
	live_ws = WebSocketPeer.new()
	live_ws.handshake_headers = PackedStringArray(["Authorization: Bearer " + Session.token])
	connect_msec = Time.get_ticks_msec()
	var ws_url := Session.base_url.replace("https://", "wss://").replace("http://", "ws://") + "/api/battle/live"
	if live_ws.connect_to_url(ws_url) != OK:
		_fail("Не удалось открыть соединение боя")


func _poll_live() -> void:
	if live_ws == null or finished or failed:
		return
	live_ws.poll()
	var ready := live_ws.get_ready_state()
	if ready == WebSocketPeer.STATE_OPEN:
		if not connected_reported:
			connected_reported = true
			print("BATTLE_LIVE_CONNECTED")
		while live_ws.get_available_packet_count() > 0:
			var data: Variant = JSON.parse_string(live_ws.get_packet().get_string_from_utf8())
			if data is Dictionary:
				_on_live(data)
		_maybe_smoke_drop()
	elif ready == WebSocketPeer.STATE_CLOSED:
		if live_done:
			return
		if not connected_reported and Time.get_ticks_msec() - connect_msec < 8000:
			return
		reconnects += 1
		if reconnects > 4:
			_fail("Соединение с боем оборвалось")
			return
		_connect_live()


func _maybe_smoke_drop() -> void:
	if not smoke or smoke_dropped or not command_reported or live_done or live_ws == null:
		return
	smoke_dropped = true
	live_ws.close()


func _on_live(msg: Dictionary) -> void:
	match str(msg.get("type", "")):
		"hello":
			if bool(msg.get("needStart", false)) and not start_sent and live_ws != null:
				start_sent = true
				live_ws.send_text(JSON.stringify({"type": "start"}))
		"state":
			_on_state(msg)
		"tick":
			_on_tick(msg)
		"auto":
			auto_on = bool(msg.get("auto", auto_on))
			_style_auto_button()
			_log_speed_ui()
		"error":
			var code := str(msg.get("error", ""))
			if code == "busy":
				_fail("Сервер занят, бой не начат")
			elif code == "internal":
				_fail("Сервер не создал бой")
		"command":
			if not command_reported:
				command_reported = true
				print("BATTLE_LIVE_COMMAND accepted=%s" % str(bool(msg.get("accepted", false))).to_lower())


func _on_state(msg: Dictionary) -> void:
	var st: Dictionary = msg.get("state", {})
	server_t = int(st.get("t", 0))
	var resumed := bool(msg.get("resumed", false))
	var new_id := str(msg.get("battleId", ""))
	if spawned and new_id != "" and new_id != battle_id and not resumed:
		_reset_battle_view()
	if new_id != "":
		battle_id = new_id
	auto_on = bool(msg.get("auto", true))
	_style_auto_button()
	if resumed and smoke_dropped and not resume_seen:
		resume_seen = true
		print("BATTLE_LIVE_RESUME resumed=true")
	if not spawned:
		var roster: Array = msg.get("roster", [])
		if roster.is_empty():
			_fail("Сервер не прислал состав боя")
			return
		for d in roster:
			_spawn_unit(d)
		spawned = true
		_apply_playback(msg)
		_snap_units(st.get("units", []))
		status_label.hide()
		playing = true
		print("BATTLE_LOADED units=", units.size(), " events=", events.size(), " balance=", str(msg.get("balanceVersion", "?")), " speed=", selected_speed, " allowed_count=", allowed_speeds.size())
	elif resumed:
		_snap_units(st.get("units", []))
		_skip_events_through(server_t)
	if st.get("result") is Dictionary:
		_mark_done(st)


func _on_tick(msg: Dictionary) -> void:
	saw_tick = true
	if not tick_reported:
		tick_reported = true
		print("BATTLE_LIVE_TICK")
	var batch: Variant = msg.get("events", [])
	if batch is Array:
		for e in batch:
			events.append(e)
	var st: Dictionary = msg.get("state", {})
	if not st.is_empty():
		server_t = int(st.get("t", server_t))
		_maybe_command(st)
	if bool(msg.get("done", false)) or st.get("result") is Dictionary:
		_mark_done(st)


func _maybe_command(st: Dictionary) -> void:
	if command_sent or live_ws == null or live_ws.get_ready_state() != WebSocketPeer.STATE_OPEN:
		return
	for raw in st.get("units", []):
		if not (raw is Dictionary):
			continue
		var id := str(raw.get("id", ""))
		if units.has(id) and str(units[id]["def"].get("side", "")) == "player" and not bool(raw.get("dead", false)):
			command_sent = true
			live_ws.send_text(JSON.stringify({"type": "main_skill", "unitId": id}))
			return


func _request_main_skill(unit_id: String) -> void:
	if live_ws == null or live_ws.get_ready_state() != WebSocketPeer.STATE_OPEN:
		status_label.text = "Нет соединения с боем"
		status_label.show()
		return
	command_sent = true
	live_ws.send_text(JSON.stringify({"type": "main_skill", "unitId": unit_id}))


func _mark_done(st: Dictionary) -> void:
	if live_done:
		return
	var res: Variant = st.get("result", {})
	if not (res is Dictionary) or (res as Dictionary).is_empty():
		return
	result = res
	final_positions = {}
	for raw in st.get("units", []):
		if raw is Dictionary and raw.has("position"):
			final_positions[str(raw["id"])] = raw["position"]
	live_done = true


func _snap_units(list: Array) -> void:
	for raw in list:
		if not (raw is Dictionary) or not units.has(str(raw.get("id", ""))):
			continue
		var u: Dictionary = units[str(raw["id"])]
		u["hp"] = int(raw.get("hp", u["hp"]))
		u["max"] = int(raw.get("maxHp", u["max"]))
		u["dead"] = bool(raw.get("dead", false))
		u["energy"] = int(raw.get("energy", 0))
		if raw.has("position"):
			u["pos"] = _server_pos(raw["position"])
			(u["node"] as Node3D).position = u["pos"]
			u["move"] = {}
		var ready: Variant = raw.get("autoReadyAt", [])
		if ready is Array:
			for i in range(mini(2, (ready as Array).size())):
				u["auto_ready_at"][i] = int(ready[i])
		_update_bar(u)
		(u["node"] as Node3D).visible = not u["dead"]
		if portraits.has(str(raw["id"])):
			var p: Dictionary = portraits[str(raw["id"])]
			p["bar"].value = int(u["energy"])
			p["tint"].modulate = Color(1.6, 1.6, 1.6) if int(u["energy"]) >= 100 else Color.WHITE


func _skip_events_through(t: int) -> void:
	while idx < events.size() and int(events[idx]["t"]) <= t:
		idx += 1


func _reset_battle_view() -> void:
	for id in units.keys():
		var n: Node = units[id]["node"]
		if is_instance_valid(n):
			n.queue_free()
	units.clear()
	for child in portrait_box.get_children():
		child.queue_free()
	portraits.clear()
	events.clear()
	idx = 0
	t_ms = 0.0
	server_t = 0
	playing = false
	finished = false
	spawned = false
	live_done = false
	inconsistencies = 0
	movement_events = 0
	auto_skill_events[0] = 0
	auto_skill_events[1] = 0
	passive_events = 0
	final_positions = {}
	result = {}
	saw_tick = false
	command_sent = false
	command_reported = false


func _again() -> void:
	if failed or live_ws == null or live_ws.get_ready_state() != WebSocketPeer.STATE_OPEN:
		get_tree().reload_current_scene()
		return
	live_ws.send_text(JSON.stringify({"type": "start"}))



func _apply_playback(payload: Dictionary) -> void:
	var server_speed := int(payload.get("speed", 1))
	selected_speed = server_speed
	# Godot разбирает JSON-числа как float: [1.0].has(1) == false, из-за этого x1 выглядела выключенной.
	allowed_speeds = _json_ints(payload.get("allowedSpeeds", [1]), [1])
	for value in [1, 2, 4]:
		var button: Button = speed_buttons.get(value)
		if button:
			var allowed := allowed_speeds.has(value)
			button.disabled = not allowed
			_style_speed_button(button, value, allowed, value == selected_speed)
	if not smoke and OS.get_environment("LUNEXGAME_BATTLE_SPEED") == "":
		speed = float(server_speed)
	_log_speed_ui()

func _json_ints(raw: Variant, fallback: Array) -> Array:
	var out: Array = []
	if raw is Array:
		for item in raw:
			out.append(int(item))
	if out.is_empty():
		return fallback.duplicate()
	return out

func _log_speed_ui() -> void:
	# JSON-числа в Godot — float: до приведения [1.0].has(1) ложно, и x1 остаётся выключенной.
	var x1: Button = speed_buttons.get(1)
	var enabled := x1 != null and not x1.disabled
	var selected := enabled and selected_speed == 1
	print("BATTLE_SPEED_UI x1_enabled=%s x1_selected=%s" % [str(enabled).to_lower(), str(selected).to_lower()])
	print("BATTLE_AUTO_UI enabled=%s" % str(auto_on).to_lower())

func _toggle_auto() -> void:
	auto_on = not auto_on
	_style_auto_button()
	_log_speed_ui()
	if live_ws != null and live_ws.get_ready_state() == WebSocketPeer.STATE_OPEN:
		live_ws.send_text(JSON.stringify({"type": "set_auto", "auto": auto_on}))

func _style_auto_button() -> void:
	if auto_button == null:
		return
	auto_button.text = "Авто: вкл" if auto_on else "Авто: выкл"
	var bg := StyleBoxFlat.new()
	bg.bg_color = Color("294d44") if auto_on else Color("172b28")
	bg.border_color = Color("e8d9a0") if auto_on else Color("35685c")
	bg.set_border_width_all(5 if auto_on else 2)
	bg.corner_radius_top_left = 12
	bg.corner_radius_top_right = 12
	bg.corner_radius_bottom_left = 12
	bg.corner_radius_bottom_right = 12
	for state_name in ["normal", "hover", "pressed", "disabled", "focus"]:
		auto_button.add_theme_stylebox_override(state_name, bg)
	auto_button.add_theme_color_override("font_color", Color("fff4bd") if auto_on else Color("71877f"))

func _style_speed_button(button: Button, value: int, allowed: bool, selected: bool) -> void:
	var bg := StyleBoxFlat.new()
	bg.bg_color = Color("294d44") if allowed else Color("172b28")
	bg.border_color = Color("e8d9a0") if selected else Color("35685c")
	bg.set_border_width_all(5 if selected else 2)
	bg.corner_radius_top_left = 12; bg.corner_radius_top_right = 12
	bg.corner_radius_bottom_left = 12; bg.corner_radius_bottom_right = 12
	button.add_theme_stylebox_override("normal", bg)
	button.add_theme_stylebox_override("hover", bg)
	button.add_theme_stylebox_override("pressed", bg)
	button.add_theme_stylebox_override("disabled", bg)
	button.add_theme_stylebox_override("focus", bg)
	button.add_theme_color_override("font_color", Color("fff4bd") if allowed else Color("71877f"))
	button.add_theme_color_override("font_hover_color", Color.WHITE)


func _set_battle_speed(value: int) -> void:
	if not allowed_speeds.has(value):
		return
	var req := HTTPRequest.new()
	req.timeout = 10.0
	add_child(req)
	var hs := PackedStringArray(["Authorization: Bearer " + Session.token, "Content-Type: application/json"])
	if req.request(Session.base_url + "/api/battle/%s/speed" % battle_id, hs, HTTPClient.METHOD_PUT, JSON.stringify({"speed": value})) != OK:
		req.queue_free()
		return
	var res: Array = await req.request_completed
	req.queue_free()
	if res[0] != HTTPRequest.RESULT_SUCCESS or res[1] != 200:
		return
	var parsed: Variant = JSON.parse_string((res[3] as PackedByteArray).get_string_from_utf8())
	if parsed is Dictionary:
		_apply_playback(parsed)


func _fail(msg: String) -> void:
	failed = true
	finished = true
	status_label.text = msg
	again_button.text = "Повторить"
	again_button.show()
	push_error("BATTLE_FAIL: " + msg)
	if smoke:
		get_tree().quit(1)


func _slot_pos(side: String, slot: int) -> Vector3:
	var x: float = FRONT_X if slot < 2 else BACK_X
	var z: float = FRONT_Z[slot] if slot < 2 else BACK_Z[slot - 2]
	return Vector3(-x if side == "player" else x, 0.0, z)


func _spawn_unit(d: Dictionary) -> void:
	var id := str(d["id"])
	var side := str(d["side"])
	var n := Node3D.new()
	n.position = _slot_pos(side, int(d["slot"]))
	if d.has("position"):
		n.position = _server_pos(d["position"])
	add_child(n)
	var body := MeshInstance3D.new()
	var role := str(d["role"])
	if role == "tank" or role == "fighter":
		var m := CapsuleMesh.new()
		m.radius = 0.42
		m.height = 1.7
		body.mesh = m
		body.position.y = 0.85
	else:
		var m2 := BoxMesh.new()
		m2.size = Vector3(0.8, 1.4, 0.6)
		body.mesh = m2
		body.position.y = 0.7
	var mat := StandardMaterial3D.new()
	var c: Color = ROLE_COLORS.get(role, Color.GRAY)
	mat.albedo_color = c if side == "player" else c.darkened(0.45)
	mat.emission_enabled = true
	mat.emission = Color.BLACK
	body.material_override = mat
	n.add_child(body)
	n.rotation.y = 0.0
	var name_l := Label3D.new()
	name_l.text = str(d["name"])
	name_l.billboard = BaseMaterial3D.BILLBOARD_ENABLED
	name_l.pixel_size = 0.004
	name_l.font_size = 30
	name_l.position = Vector3(0, 2.75 + float(int(d["slot"]) % 2) * 0.22, 0)
	name_l.visible = false # в бою роль и цвет читаются по модели; числа и полосы не перекрываются именами
	name_l.modulate = Color("e8d9a0") if side == "player" else Color("e0a0a0")
	n.add_child(name_l)
	var bg := MeshInstance3D.new()
	var bgm := BoxMesh.new()
	bgm.size = Vector3(1.2, 0.14, 0.04)
	bg.mesh = bgm
	var bgmat := StandardMaterial3D.new()
	bgmat.albedo_color = Color("221111")
	bg.material_override = bgmat
	bg.position = Vector3(0, 2.28, 0)
	n.add_child(bg)
	var fill := MeshInstance3D.new()
	var fm := BoxMesh.new()
	fm.size = Vector3(1.2, 0.14, 0.05)
	fill.mesh = fm
	var fmat := StandardMaterial3D.new()
	fmat.albedo_color = Color("3fe0c5") if side == "player" else Color("d9534f")
	fill.material_override = fmat
	fill.position = Vector3(0, 2.28, 0.01)
	n.add_child(fill)
	units[id] = {
		"def": d, "node": n, "mat": mat, "fill": fill, "home": n.position,
		"pos": n.position, "move": {}, "body": body, "action": null,
		"hp": int(d["maxHp"]), "max": int(d["maxHp"]), "dead": false, "energy": 0,
		"passive_seen": false, "auto_ready_at": [0, 0],
	}
	var skills: Array = d.get("autoSkills", [])
	for slot in range(mini(2, skills.size())):
		units[id]["auto_ready_at"][slot] = int(skills[slot].get("cooldownMs", 0))
	if side == "player":
		_make_portrait(d)


func _process(delta: float) -> void:
	_poll_live()
	if not playing or finished:
		return
	var cap := float(server_t)
	if live_done and result.has("durationMs"):
		cap = maxf(cap, float(int(result["durationMs"])))
	var advance := delta * 1000.0 * maxf(speed, 1.0)
	t_ms = minf(cap, t_ms + advance)
	if cap - t_ms > 250.0:
		t_ms = cap
	timer_label.text = "%d:%02d / 1:30" % [int(t_ms / 60000), int(fmod(t_ms / 1000.0, 60.0))]
	while idx < events.size() and float(events[idx]["t"]) <= t_ms:
		_apply(events[idx])
		idx += 1
	_advance_positions(t_ms)
	if live_done and idx >= events.size() and t_ms >= float(int(result.get("durationMs", 0))):
		_finish()


func _tween() -> Tween:
	var tw := create_tween()
	tw.set_speed_scale(speed)
	return tw


func _apply(e: Dictionary) -> void:
	# События могут идти по нескольку тиков за кадр: сначала доводим координаты до времени события.
	_advance_positions(float(e["t"]))
	var type := str(e["type"])
	var src := str(e["src"])
	if not units.has(src):
		inconsistencies += 1
		return
	var s: Dictionary = units[src]
	if type == "passive":
		# Эффективные MaxHP/Attack уже пришли в описании героя; повторно бонус не применяем.
		var passive: Dictionary = s["def"].get("passive", {})
		if not s["def"].get("passiveActive", false) or s["passive_seen"] or int(e["t"]) != 0:
			inconsistencies += 1
		if passive.get("id", "") != e.get("skillId", "") or int(s["def"].get("level", 1)) < int(passive.get("unlockLevel", 40)):
			inconsistencies += 1
		s["passive_seen"] = true
		passive_events += 1
		return
	if type == "death":
		s["dead"] = true
		s["move"] = {}
		var tw := _tween()
		tw.tween_property(s["node"], "scale", Vector3(1, 0.1, 1), 0.4)
		tw.tween_callback(func() -> void: s["node"].visible = false)  # павший уходит с поля вместе с подписью
		return
	if s["dead"]:
		inconsistencies += 1  # мёртвый юнит не может действовать
	if type == "move":
		var motion: Dictionary = e["move"]
		if s["pos"].distance_to(_server_pos(motion["from"])) > 0.002 or not s["def"]["melee"]:
			inconsistencies += 1
		s["move"] = {"from": _server_pos(motion["from"]), "to": _server_pos(motion["to"]),
			"t": int(e["t"]), "duration": int(motion["durationMs"])}
		movement_events += 1
		return
	var targets: Array = e.get("targets", [])
	var is_skill := type == "skill" or type == "auto_skill"
	var heal := is_skill and str(e.get("kind", "")) == "heal"
	if type == "auto_skill":
		var slot := int(e.get("skillSlot", 0))
		var skills: Array = s["def"].get("autoSkills", [])
		if slot < 1 or slot > 2 or skills.size() < slot:
			inconsistencies += 1
		else:
			var skill: Dictionary = skills[slot - 1]
			if skill.get("id", "") != e.get("skillId", "") or int(s["def"].get("level", 1)) < int(skill.get("unlockLevel", 1000)):
				inconsistencies += 1
			if int(e["t"]) < int(s["auto_ready_at"][slot - 1]):
				inconsistencies += 1
			s["auto_ready_at"][slot - 1] = int(e["t"]) + int(skill["cooldownMs"])
			auto_skill_events[slot - 1] += 1
	_play_action(s, type, targets, heal)
	for h in targets:
		var tid := str(h["unit"])
		if not units.has(tid):
			inconsistencies += 1
			continue
		var t: Dictionary = units[tid]
		if s["def"].has("attackRange") and s["pos"].distance_to(t["pos"]) > float(s["def"]["attackRange"]) / 1000.0 + 0.002:
			inconsistencies += 1
		var is_heal := heal or (type == "skill" and tid == src)  # лечение себя в навыке танка
		var hp := int(h["hp"])
		if hp < 0 or hp > int(t["max"]):
			inconsistencies += 1
		t["hp"] = hp
		_update_bar(t)
		_float_text(t["node"], ("+%d" if is_heal and int(h["amount"]) > 0 and heal_target_is_ally(s, t) else "-%d") % int(h["amount"]),
			Color("6ef08a") if is_heal and heal_target_is_ally(s, t) else Color("ff6a5a"))
		if not (is_heal and heal_target_is_ally(s, t)):
			_flash(t)
	for id in e.get("energy", {}):
		if units.has(id):
			units[id]["energy"] = int(e["energy"][id])
			if portraits.has(id):
				var p: Dictionary = portraits[id]
				p["bar"].value = int(e["energy"][id])
				p["tint"].modulate = Color(1.6, 1.6, 1.6) if int(e["energy"][id]) >= 100 else Color.WHITE


func heal_target_is_ally(s: Dictionary, t: Dictionary) -> bool:
	return str(s["def"]["side"]) == str(t["def"]["side"])


func _server_pos(p: Dictionary) -> Vector3:
	return Vector3(float(p["x"]) / 1000.0, 0.0, float(p["z"]) / 1000.0)


func _advance_positions(at_ms: float) -> void:
	for id in units:
		var u: Dictionary = units[id]
		var motion: Dictionary = u["move"]
		if motion.is_empty():
			continue
		var weight := clampf((at_ms - float(motion["t"])) / float(motion["duration"]), 0.0, 1.0)
		u["pos"] = (motion["from"] as Vector3).lerp(motion["to"], weight)
		u["node"].position = u["pos"]
		if weight >= 1.0:
			u["move"] = {}


func _play_action(s: Dictionary, type: String, targets: Array, _heal: bool) -> void:
	var n: Node3D = s["node"]
	var d: Dictionary = s["def"]
	var dst := Vector3.ZERO
	if targets.size() > 0 and units.has(str(targets[0]["unit"])) and str(targets[0]["unit"]) != str(d["id"]):
		dst = units[str(targets[0]["unit"])]["pos"]
	if type == "skill" or type == "auto_skill":
		var tw := _tween()
		tw.tween_property(s["mat"], "emission", Color(0.9, 0.8, 0.2), 0.15)
		tw.tween_property(s["mat"], "emission", Color.BLACK, 0.35)
		var tw2 := _tween()
		tw2.tween_property(n, "scale", Vector3(1.25, 1.25, 1.25), 0.15)
		tw2.tween_property(n, "scale", Vector3.ONE, 0.25)
	if dst == Vector3.ZERO:
		return
	if d["melee"]:
		# Короткий выпад меша; корень героя движется только по записи сервера.
		var dir: Vector3 = (dst - s["pos"]).normalized()
		var body: MeshInstance3D = s["body"]
		var rest := Vector3(0, 0.85, 0)
		if s["action"] != null:
			(s["action"] as Tween).kill()
		var tw3 := _tween()
		s["action"] = tw3
		tw3.tween_property(body, "position", rest + dir * 0.12, 0.1)
		tw3.tween_property(body, "position", rest, 0.15)
	else:
		var ball := MeshInstance3D.new()
		var sm := SphereMesh.new()
		sm.radius = 0.14
		sm.height = 0.28
		ball.mesh = sm
		var bm := StandardMaterial3D.new()
		bm.albedo_color = ROLE_COLORS.get(str(d["role"]), Color.WHITE)
		bm.emission_enabled = true
		bm.emission = bm.albedo_color
		ball.material_override = bm
		ball.position = s["pos"] + Vector3(0, 1.2, 0)
		add_child(ball)
		var tw4 := _tween()
		tw4.tween_property(ball, "position", dst + Vector3(0, 1.2, 0), 0.25)
		tw4.tween_callback(ball.queue_free)


func _flash(t: Dictionary) -> void:
	var tw := _tween()
	tw.tween_property(t["mat"], "emission", Color(0.8, 0.1, 0.1), 0.06)
	tw.tween_property(t["mat"], "emission", Color.BLACK, 0.2)


func _update_bar(t: Dictionary) -> void:
	var ratio: float = clampf(float(t["hp"]) / float(t["max"]), 0.0, 1.0)
	var f: MeshInstance3D = t["fill"]
	f.scale.x = maxf(ratio, 0.001)
	f.position.x = -0.6 * (1.0 - ratio)


func _float_text(node: Node3D, text: String, color: Color) -> void:
	var l := Label3D.new()
	l.text = text
	l.billboard = BaseMaterial3D.BILLBOARD_ENABLED
	l.pixel_size = 0.008
	l.font_size = 64
	l.modulate = color
	l.position = node.position + Vector3(0, 2.7, 0.3)
	add_child(l)
	var tw := _tween()
	tw.tween_property(l, "position:y", l.position.y + 1.0, 0.7)
	tw.parallel().tween_property(l, "modulate:a", 0.0, 0.7)
	tw.tween_callback(l.queue_free)


func _finish() -> void:
	if finished:
		return
	finished = true
	var player_alive := 0
	var enemy_alive := 0
	for id in units:
		if final_positions.has(id) and units[id]["pos"].distance_to(_server_pos(final_positions[id])) > 0.002:
			inconsistencies += 1
		if not units[id]["dead"]:
			# Возврат после завершения боя — только победная стойка, на результат не влияет.
			var return_tween := _tween()
			return_tween.tween_property(units[id]["node"], "position", units[id]["home"], 0.7)
			if str(units[id]["def"]["side"]) == "player":
				player_alive += 1
			else:
				enemy_alive += 1
	var winner := str(result["winner"])
	# Сверка результата сервера с проигранным состоянием.
	if str(result["reason"]) == "elimination":
		if winner == "player" and (enemy_alive != 0 or player_alive == 0):
			inconsistencies += 1
		if winner == "enemy" and (player_alive != 0 or enemy_alive == 0):
			inconsistencies += 1
	result_label.text = "ПОБЕДА" if winner == "player" else "ПОРАЖЕНИЕ"
	result_label.show()
	again_button.show()
	await _confirm_saved_result()
	if not final_positions.is_empty():
		print("BATTLE_MOVEMENT_OK moves=%d inconsistencies=%d" % [movement_events, inconsistencies])
	print("BATTLE_SKILLS_OK auto1=%d auto2=%d passives=%d inconsistencies=%d" % [
		auto_skill_events[0], auto_skill_events[1], passive_events, inconsistencies])
	print("BATTLE_OK winner=%s reason=%s durationMs=%d events=%d inconsistencies=%d" % [
		winner, str(result["reason"]), int(result["durationMs"]), events.size(), inconsistencies])
	if smoke:
		get_tree().quit(0 if inconsistencies == 0 else 1)


func _confirm_saved_result() -> void:
	# Запись нужна для сверки результата, а не для проигрывания.
	if battle_id == "":
		inconsistencies += 1
		return
	var req := HTTPRequest.new()
	req.timeout = 10.0
	add_child(req)
	var hs := PackedStringArray(["Authorization: Bearer " + Session.token])
	if req.request(Session.base_url + "/api/battle/" + battle_id, hs, HTTPClient.METHOD_GET) != OK:
		inconsistencies += 1
		req.queue_free()
		return
	var res: Array = await req.request_completed
	req.queue_free()
	if int(res[1]) != 200:
		inconsistencies += 1
		return
	var parsed: Variant = JSON.parse_string((res[3] as PackedByteArray).get_string_from_utf8())
	if not (parsed is Dictionary):
		inconsistencies += 1
		return
	var battle: Variant = (parsed as Dictionary).get("battle", {})
	if not (battle is Dictionary):
		inconsistencies += 1
		return
	var saved: Variant = (battle as Dictionary).get("result", {})
	if not (saved is Dictionary) or str((saved as Dictionary).get("winner", "")) != str(result.get("winner", "")):
		inconsistencies += 1
