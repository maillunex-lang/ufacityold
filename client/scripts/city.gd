extends Control

var base: VBoxContainer
var content: Label
var afk_button: Button
var hero_rows: Array[Node] = []
var campaign_rows: Array[Node] = []
var campaign_box: VBoxContainer
var demo := false

func _ready() -> void:
	demo = OS.get_environment("LUNEXGAME_CITY_DEMO") == "1"
	var bg:=ColorRect.new(); bg.color=Color("06251f"); bg.set_anchors_preset(Control.PRESET_FULL_RECT); add_child(bg)
	var safe:=MarginContainer.new(); safe.set_anchors_preset(Control.PRESET_FULL_RECT); var i:=SafeArea.insets(get_viewport()); safe.add_theme_constant_override("margin_left",20+int(i.left)); safe.add_theme_constant_override("margin_right",20+int(i.right)); safe.add_theme_constant_override("margin_top",20+int(i.top)); safe.add_theme_constant_override("margin_bottom",30+int(i.bottom)); add_child(safe)
	base=VBoxContainer.new(); base.add_theme_constant_override("separation",20); safe.add_child(base)
	var title:=Label.new(); title.text="Самрая · город"; title.add_theme_font_size_override("font_size",54); title.add_theme_color_override("font_color",Color("d4af37")); base.add_child(title)
	var tabs:=HBoxContainer.new(); base.add_child(tabs)
	for item in ["Город","Кампания","Герои","Инвентарь"]:
		var b:=Button.new(); b.text=item; b.custom_minimum_size=Vector2(0,90); b.size_flags_horizontal=Control.SIZE_EXPAND_FILL; b.add_theme_font_size_override("font_size",28); b.pressed.connect(func()->void: _open(item)); tabs.add_child(b)
	content=Label.new(); content.autowrap_mode=TextServer.AUTOWRAP_WORD_SMART; content.add_theme_font_size_override("font_size",30); content.add_theme_color_override("font_color",Color("e8d9a0")); base.add_child(content)
	campaign_box=VBoxContainer.new(); campaign_box.add_theme_constant_override("separation",10); base.add_child(campaign_box)
	_open("Город")
	if demo: get_tree().create_timer(1.0).timeout.connect(func()->void: _open("Кампания"))

func _open(item:String)->void:
	if item=="Кампания": await _campaign()
	elif item=="Герои": await _heroes()
	elif item=="Инвентарь": await _home(true)
	else: await _home(false)

func _home(inventory:bool)->void:
	var h:=await _request("/api/home"); if h.is_empty(): content.text="Не удалось загрузить город"; return
	_clear_hero_rows()
	_clear_campaign_rows()
	content.text="Золото: %d\nОпыт героя: %d\nПройдено этапов: %d\n\n%s"%[int(h.get("gold",0)),int(h.get("heroXp",0)),int(h.get("campaignCleared",0)),"Инвентарь" if inventory else "Выберите раздел ниже."]
	if afk_button:
		afk_button.queue_free(); afk_button=null
	if inventory:
		afk_button=Button.new(); afk_button.text="Забрать AFK-награду"; afk_button.custom_minimum_size=Vector2(0,90); afk_button.add_theme_font_size_override("font_size",28); afk_button.pressed.connect(_claim_afk); base.add_child(afk_button)
	if OS.get_environment("LUNEXGAME_SMOKE_CITY")=="1": print("CITY_OK gold=",int(h.get("gold",0))," heroes=",h.get("heroes",[]).size()," cleared=",int(h.get("campaignCleared",0)))

func _claim_afk()->void:
	var result:=await _post("/api/afk/claim")
	if result.is_empty(): content.text="Не удалось получить AFK-награду"; return
	content.text="AFK-награда получена: +%d золота\nНакоплено секунд: %d"%[int(result.get("grantedGold",0)),int(result.get("elapsedSeconds",0))]
	await get_tree().create_timer(1.0).timeout
	await _home(true)

func _campaign()->void:
	var c:=await _request("/api/campaign"); if c.is_empty(): content.text="Не удалось загрузить кампанию"; return
	_clear_hero_rows(); _clear_campaign_rows()
	var lines:PackedStringArray=["Кампания"]
	for s in c.get("stages",[]):
		var id:=int(s.get("id",0)); var open:=bool(s.get("open",false)); lines.append("%s  %s"%[str(s.get("name","Этап")),"ДОСТУПЕН" if open else "закрыт"])
		if open:
			var row:=HBoxContainer.new(); var label:=Label.new(); label.text=str(s.get("name","Этап")); label.add_theme_font_size_override("font_size",26); label.size_flags_horizontal=Control.SIZE_EXPAND_FILL; row.add_child(label)
			var fight:=Button.new(); fight.text="Сражаться"; fight.custom_minimum_size=Vector2(0,64); fight.add_theme_font_size_override("font_size",22); fight.pressed.connect(func()->void: _fight_stage(id)); row.add_child(fight); campaign_box.add_child(row); campaign_rows.append(row)
	content.text="\n".join(lines)
	if OS.get_environment("LUNEXGAME_SMOKE_CITY")=="1":
		print("CAMPAIGN_OK stages=",c.get("stages",[]).size()," open=",1)
		get_tree().quit(0)

func _fight_stage(stage:int)->void:
	Session.campaign_stage = stage
	get_tree().change_scene_to_file("res://scenes/battle.tscn")

func _clear_campaign_rows()->void:
	for row in campaign_rows:
		if is_instance_valid(row): row.queue_free()
	campaign_rows.clear()

func _heroes()->void:
	var h:=await _request("/api/home"); if h.is_empty(): content.text="Не удалось загрузить героев"; return
	_clear_hero_rows(); _clear_campaign_rows()
	var counts:= {}
	for state in h.get("heroStates",[]):
		var faction:=_hero_faction(str(state.get("id",""))); counts[faction]=int(counts.get(faction,0))+1
	content.text="Герои\nЗолото: %d   Опыт: %d\nФракции: %s"%[int(h.get("gold",0)),int(h.get("heroXp",0)),_formation_bonus(counts)]
	for state in h.get("heroStates",[]):
		var row:=HBoxContainer.new(); var label:=Label.new(); label.text="\n%s · уровень %d · %s"%[str(state.get("id","герой")),int(state.get("level",1)),_hero_faction_name(str(state.get("id","")))]; label.add_theme_font_size_override("font_size",26); label.size_flags_horizontal=Control.SIZE_EXPAND_FILL; row.add_child(label)
		var up:=Button.new(); up.text="Улучшить"; up.add_theme_font_size_override("font_size",22); up.pressed.connect(func()->void: _level_up(str(state.get("id","")))); row.add_child(up); base.add_child(row)
		hero_rows.append(row)

func _hero_faction(id:String)->String:
	return {"guardian":"dawn", "fighter":"dawn", "archer":"tide", "mage":"grove", "healer":"ember"}.get(id, "unknown")

func _hero_faction_name(id:String)->String:
	return {"dawn":"Рассвет", "tide":"Прилив", "grove":"Роща", "ember":"Пламя"}.get(_hero_faction(id), "Неизвестная фракция")

func _formation_bonus(counts:Dictionary)->String:
	var best:=0
	for value in counts.values(): best=maxi(best,int(value))
	if best>=5: return "бонус состава +20% атаки и здоровья"
	if best>=4: return "бонус состава +15% атаки и здоровья"
	if best>=3: return "бонус состава +10% атаки и здоровья"
	return "бонус состава не активен"

func _clear_hero_rows()->void:
	for row in hero_rows:
		if is_instance_valid(row): row.queue_free()
	hero_rows.clear()

func _level_up(id:String)->void:
	var result:=await _post("/api/heroes/"+id+"/level"); if result.is_empty(): content.text="Недостаточно золота или опыта"; return
	await _heroes()

func _post(path:String)->Dictionary:
	var r:=HTTPRequest.new()
	add_child(r)
	var h:=PackedStringArray(["Authorization: Bearer "+Session.token])
	if r.request(Session.base_url+path,h,HTTPClient.METHOD_POST)!=OK:
		r.queue_free()
		return {}
	var x:Array=await r.request_completed
	r.queue_free()
	if x[0]!=HTTPRequest.RESULT_SUCCESS or x[1]!=200:
		return {}
	var p:Variant=JSON.parse_string((x[3] as PackedByteArray).get_string_from_utf8())
	if p is Dictionary:
		return p
	return {}

func _request(path:String)->Dictionary:
	var r:=HTTPRequest.new()
	add_child(r)
	var h:=PackedStringArray(["Authorization: Bearer "+Session.token])
	if r.request(Session.base_url+path,h,HTTPClient.METHOD_GET)!=OK:
		r.queue_free()
		return {}
	var x:Array=await r.request_completed
	r.queue_free()
	if x[0]!=HTTPRequest.RESULT_SUCCESS or x[1]!=200:
		return {}
	var p:Variant=JSON.parse_string((x[3] as PackedByteArray).get_string_from_utf8())
	if p is Dictionary:
		return p
	return {}
