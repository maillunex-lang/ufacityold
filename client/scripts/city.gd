extends Control

var base: VBoxContainer
var content: Label
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
	_open("Город")
	if demo: get_tree().create_timer(1.0).timeout.connect(func()->void: _open("Кампания"))

func _open(item:String)->void:
	if item=="Кампания": await _campaign()
	elif item=="Герои": content.text="Герои\n\nСтраж\nВоин\n\nНовые герои откроются в кампании."
	elif item=="Инвентарь": await _home(true)
	else: await _home(false)

func _home(inventory:bool)->void:
	var h:=await _request("/api/home"); if h.is_empty(): content.text="Не удалось загрузить город"; return
	content.text="Золото: %d\nОпыт героя: %d\nПройдено этапов: %d\n\n%s"%[int(h.get("gold",0)),int(h.get("heroXp",0)),int(h.get("campaignCleared",0)),"Инвентарь" if inventory else "Выберите раздел ниже."]
	if OS.get_environment("LUNEXGAME_SMOKE_CITY")=="1": print("CITY_OK gold=",int(h.get("gold",0))," heroes=",h.get("heroes",[]).size()," cleared=",int(h.get("campaignCleared",0)))

func _campaign()->void:
	var c:=await _request("/api/campaign"); if c.is_empty(): content.text="Не удалось загрузить кампанию"; return
	var lines:PackedStringArray=["Кампания"]
	for s in c.get("stages",[]): lines.append("%s  %s"%[str(s.get("name","Этап")),"ДОСТУПЕН" if bool(s.get("open",false)) else "закрыт"])
	content.text="\n".join(lines)
	if OS.get_environment("LUNEXGAME_SMOKE_CITY")=="1":
		print("CAMPAIGN_OK stages=",c.get("stages",[]).size()," open=",1)
		get_tree().quit(0)

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
