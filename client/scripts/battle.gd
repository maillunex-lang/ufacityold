extends Node3D
## Прототип боя (этап 1). Сервер считает бой целиком; здесь — проигрывание записи событий.
## Модели — ВРЕМЕННЫЕ примитивы. Главная способность срабатывает автоматически (полный автомат);
## ручной запуск нажатием на портрет появится вместе с боем в реальном времени.

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
var units: Dictionary = {}  # id -> Dictionary
var t_ms: float = 0.0
var idx: int = 0
var playing: bool = false
var finished: bool = false
var inconsistencies: int = 0
var acted_dead: Dictionary = {}

var timer_label: Label
var result_label: Label
var status_label: Label
var again_button: Button
var portraits: Dictionary = {}  # id -> {bar, tint}
var portrait_box: HBoxContainer


func _ready() -> void:
	smoke = OS.get_environment("LUNEXGAME_SMOKE") == "1"
	if smoke:
		speed = SMOKE_SPEED
	if OS.get_environment("LUNEXGAME_BATTLE_SPEED") != "":
		speed = float(OS.get_environment("LUNEXGAME_BATTLE_SPEED"))  # для записи кадров/отладки
	_build_world()
	_build_ui()
	_load_battle()


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
	var root := Control.new()
	root.set_anchors_preset(Control.PRESET_FULL_RECT)
	root.mouse_filter = Control.MOUSE_FILTER_IGNORE
	layer.add_child(root)
	timer_label = _ui_label("0:00 / 1:30", 48, Color("e8d9a0"))
	timer_label.set_anchors_preset(Control.PRESET_CENTER_TOP)
	timer_label.position = Vector2(-150, 40)
	root.add_child(timer_label)
	var warn := _ui_label("ВРЕМЕННЫЕ МОДЕЛИ И БАЛАНС (прототип)", 28, Color("3fe0c5"))
	warn.set_anchors_preset(Control.PRESET_CENTER_TOP)
	warn.position = Vector2(-300, 110)
	root.add_child(warn)
	status_label = _ui_label("Загрузка боя…", 40, Color("e8d9a0"))
	status_label.set_anchors_preset(Control.PRESET_CENTER)
	status_label.position = Vector2(-250, -40)
	root.add_child(status_label)
	result_label = _ui_label("", 96, Color("d4af37"))
	result_label.set_anchors_preset(Control.PRESET_CENTER)
	result_label.position = Vector2(-300, -520)
	result_label.hide()
	root.add_child(result_label)
	again_button = Button.new()
	again_button.text = "Ещё бой"
	again_button.custom_minimum_size = Vector2(420, 120)
	again_button.add_theme_font_size_override("font_size", 48)
	again_button.set_anchors_preset(Control.PRESET_CENTER)
	again_button.position = Vector2(-210, 140)
	again_button.pressed.connect(func() -> void: get_tree().reload_current_scene())
	again_button.hide()
	root.add_child(again_button)
	var bar := HBoxContainer.new()
	bar.set_anchors_preset(Control.PRESET_BOTTOM_WIDE)
	bar.offset_top = -330
	bar.offset_bottom = -40
	bar.offset_left = 20
	bar.offset_right = -20
	bar.add_theme_constant_override("separation", 12)
	root.add_child(bar)
	portrait_box = bar


func _ui_label(text: String, size: int, color: Color) -> Label:
	var l := Label.new()
	l.text = text
	l.add_theme_font_size_override("font_size", size)
	l.add_theme_color_override("font_color", color)
	l.horizontal_alignment = HORIZONTAL_ALIGNMENT_CENTER
	l.custom_minimum_size = Vector2(300, 0)
	return l


func _make_portrait(def: Dictionary) -> void:
	var box := VBoxContainer.new()
	box.size_flags_horizontal = Control.SIZE_EXPAND_FILL
	var tint := ColorRect.new()
	tint.color = ROLE_COLORS.get(str(def["role"]), Color.GRAY)
	tint.custom_minimum_size = Vector2(0, 150)
	box.add_child(tint)
	var name_l := _ui_label(str(def["name"]), 26, Color.WHITE)
	name_l.custom_minimum_size = Vector2(0, 0)
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
	portrait_box.add_child(box)
	portraits[str(def["id"])] = {"bar": en, "tint": tint}


func _load_battle() -> void:
	var req := HTTPRequest.new()
	req.timeout = 20.0
	add_child(req)
	var hs := PackedStringArray(["Authorization: Bearer " + Session.token])
	if req.request(Session.base_url + "/api/battle/start", hs, HTTPClient.METHOD_POST, "") != OK:
		_fail("Не удалось отправить запрос боя")
		return
	var res: Array = await req.request_completed
	req.queue_free()
	if res[0] != HTTPRequest.RESULT_SUCCESS or res[1] != 200:
		_fail("Сервер не выдал бой (код %d)" % int(res[1]))
		return
	var parsed: Variant = JSON.parse_string((res[3] as PackedByteArray).get_string_from_utf8())
	if not (parsed is Dictionary) or not parsed.has("battle"):
		_fail("Некорректный ответ сервера")
		return
	var b: Dictionary = parsed["battle"]
	events = b["events"]
	result = b["result"]
	for d in b["units"]:
		_spawn_unit(d)
	status_label.hide()
	playing = true
	print("BATTLE_LOADED units=", units.size(), " events=", events.size(), " balance=", b.get("balanceVersion", "?"))


func _fail(msg: String) -> void:
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
	name_l.pixel_size = 0.006
	name_l.font_size = 48
	name_l.position = Vector3(0, 2.55, 0)
	name_l.modulate = Color("e8d9a0") if side == "player" else Color("e0a0a0")
	n.add_child(name_l)
	var bg := MeshInstance3D.new()
	var bgm := BoxMesh.new()
	bgm.size = Vector3(1.2, 0.14, 0.04)
	bg.mesh = bgm
	var bgmat := StandardMaterial3D.new()
	bgmat.albedo_color = Color("221111")
	bg.material_override = bgmat
	bg.position = Vector3(0, 2.25, 0)
	n.add_child(bg)
	var fill := MeshInstance3D.new()
	var fm := BoxMesh.new()
	fm.size = Vector3(1.2, 0.14, 0.05)
	fill.mesh = fm
	var fmat := StandardMaterial3D.new()
	fmat.albedo_color = Color("3fe0c5") if side == "player" else Color("d9534f")
	fill.material_override = fmat
	fill.position = Vector3(0, 2.25, 0.01)
	n.add_child(fill)
	units[id] = {
		"def": d, "node": n, "mat": mat, "fill": fill, "home": n.position,
		"hp": int(d["maxHp"]), "max": int(d["maxHp"]), "dead": false, "energy": 0,
	}
	if side == "player":
		_make_portrait(d)


func _process(delta: float) -> void:
	if not playing or finished:
		return
	t_ms += delta * 1000.0 * speed
	timer_label.text = "%d:%02d / 1:30" % [int(t_ms / 60000), int(fmod(t_ms / 1000.0, 60.0))]
	while idx < events.size() and float(events[idx]["t"]) <= t_ms:
		_apply(events[idx])
		idx += 1
	if idx >= events.size() and t_ms >= float(result["durationMs"]) + 800.0:
		_finish()


func _tween() -> Tween:
	var tw := create_tween()
	tw.set_speed_scale(speed)
	return tw


func _apply(e: Dictionary) -> void:
	var type := str(e["type"])
	var src := str(e["src"])
	if not units.has(src):
		inconsistencies += 1
		return
	var s: Dictionary = units[src]
	if type == "death":
		s["dead"] = true
		var tw := _tween()
		tw.tween_property(s["node"], "scale", Vector3(1, 0.1, 1), 0.4)
		tw.tween_callback(func() -> void: s["node"].visible = false)  # павший уходит с поля вместе с подписью
		return
	if s["dead"]:
		inconsistencies += 1  # мёртвый юнит не может действовать
	var targets: Array = e.get("targets", [])
	var heal := type == "skill" and str(e.get("kind", "")) == "heal"
	_play_action(s, type, targets, heal)
	for h in targets:
		var tid := str(h["unit"])
		if not units.has(tid):
			inconsistencies += 1
			continue
		var t: Dictionary = units[tid]
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


func _play_action(s: Dictionary, type: String, targets: Array, _heal: bool) -> void:
	var n: Node3D = s["node"]
	var d: Dictionary = s["def"]
	var dst := Vector3.ZERO
	if targets.size() > 0 and units.has(str(targets[0]["unit"])) and str(targets[0]["unit"]) != str(d["id"]):
		dst = units[str(targets[0]["unit"])]["home"]
	if type == "skill":
		var tw := _tween()
		tw.tween_property(s["mat"], "emission", Color(0.9, 0.8, 0.2), 0.15)
		tw.tween_property(s["mat"], "emission", Color.BLACK, 0.35)
		var tw2 := _tween()
		tw2.tween_property(n, "scale", Vector3(1.25, 1.25, 1.25), 0.15)
		tw2.tween_property(n, "scale", Vector3.ONE, 0.25)
	if dst == Vector3.ZERO:
		return
	if d["melee"]:
		var dir: Vector3 = (dst - s["home"]).normalized()
		var tw3 := _tween()
		tw3.tween_property(n, "position", dst - dir * 1.1, 0.18)
		tw3.tween_property(n, "position", s["home"], 0.25)
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
		ball.position = s["home"] + Vector3(0, 1.2, 0)
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
	finished = true
	var player_alive := 0
	var enemy_alive := 0
	for id in units:
		if not units[id]["dead"]:
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
	print("BATTLE_OK winner=%s reason=%s durationMs=%d events=%d inconsistencies=%d" % [
		winner, str(result["reason"]), int(result["durationMs"]), events.size(), inconsistencies])
	if smoke:
		get_tree().quit(0 if inconsistencies == 0 else 1)
