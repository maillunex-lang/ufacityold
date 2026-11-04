class_name SafeArea
## Безопасная зона экрана (вырез камеры, скруглённые углы, системные жесты).
## Возвращает отступы в координатах окна интерфейса (после растяжения), чтобы не класть важное под вырез.

static func insets(vp: Viewport) -> Dictionary:
	var d := {"left": 0.0, "top": 0.0, "right": 0.0, "bottom": 0.0}
	var win := Vector2(DisplayServer.window_get_size())
	var vis := vp.get_visible_rect().size
	if win.x > 0.0 and win.y > 0.0:
		var area := DisplayServer.get_display_safe_area()
		if area.size.x > 0 and area.size.y > 0:
			var sx := vis.x / win.x
			var sy := vis.y / win.y
			d["left"] = maxf(float(area.position.x) * sx, 0.0)
			d["top"] = maxf(float(area.position.y) * sy, 0.0)
			d["right"] = maxf((win.x - float(area.end.x)) * sx, 0.0)
			d["bottom"] = maxf((win.y - float(area.end.y)) * sy, 0.0)
	# Проверка раскладки на ПК: LUNEXGAME_FAKE_SAFE_TOP=<px> имитирует вырез сверху.
	var fake := OS.get_environment("LUNEXGAME_FAKE_SAFE_TOP")
	if fake != "":
		d["top"] = float(fake)
	return d
