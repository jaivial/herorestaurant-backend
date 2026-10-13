package api

// Coordination id: ads_canvas_v1
// Cross-origin embed API + JS plugin for businesses whose website is not
// built on our templates: they paste one <script> tag and the plugin renders
// their active canvas ads in iframes served by this backend.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// handlePublicAdsEmbed serves GET /public/ads/embed?restaurant_id=X[&date=YYYY-MM-DD].
// CORS-open read-only endpoint: returns active canvas ads with their render URLs.
func (s *Server) handlePublicAdsEmbed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	restaurantID := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("restaurant_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid restaurant_id"})
			return
		}
		restaurantID = id
	} else if id, ok := s.resolveRestaurantID(r); ok {
		restaurantID = id
	}
	if restaurantID <= 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	rows, err := s.db.QueryContext(r.Context(), `SELECT id FROM restaurant_ads WHERE restaurant_id = ? AND active = 1 AND (canvas_json IS NOT NULL OR layout_json IS NOT NULL) ORDER BY updated_at DESC, id DESC`, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ads")
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	type embedVariant struct {
		Container *boAdCanvasContainer `json:"container,omitempty"`
		Width     int                  `json:"width"`
		Height    int                  `json:"height"`
	}
	type embedAd struct {
		Steps     []boAdStep `json:"steps,omitempty"`
		CTAs      []boAdCTA  `json:"ctas,omitempty"`
		ID        int64      `json:"id"`
		Name      string     `json:"name"`
		Width     int        `json:"width"`
		Height    int        `json:"height"`
		RenderURL string     `json:"render_url"`
		// Coord id ads_canvas_breakpoints_v1: responsive dims + popover chrome
		// so the plugin can pick a variant by viewport like the home popover does.
		Variants  map[string]embedVariant `json:"variants,omitempty"`
		Container *boAdCanvasContainer    `json:"container,omitempty"`
	}
	ads := []embedAd{}
	for _, id := range ids {
		ad, err := s.readBOAd(r.Context(), restaurantID, id)
		if err != nil || (ad.Canvas == nil && (ad.Layout == nil || ad.Layout.Mode != "multiple")) {
			continue
		}
		if !publicAdVisibleOnDate(ad, date) {
			continue
		}
		item := embedAd{ID: ad.ID, Name: ad.Name, RenderURL: fmt.Sprintf("/api/public/ads/%d/render?restaurant_id=%d&src=plugin", ad.ID, restaurantID)}
		if ad.Layout != nil && ad.Layout.Mode == "multiple" {
			for _, step := range ad.Layout.Steps {
				item.Steps = append(item.Steps, step)
			}
			item.CTAs = ad.CTAs
			item.Width, item.Height = 560, 600
			ads = append(ads, item)
			continue
		}
		item.Width, item.Height = ad.Canvas.Width, ad.Canvas.Height
		for name, variant := range ad.Canvas.Variants {
			if variant == nil || variant.Width <= 0 || variant.Height <= 0 {
				continue
			}
			if item.Variants == nil {
				item.Variants = map[string]embedVariant{}
			}
			item.Variants[name] = embedVariant{Width: variant.Width, Height: variant.Height, Container: variant.Container}
		}
		if ad.Canvas.Container != nil {
			c := *ad.Canvas.Container
			item.Container = &c
		}
		ads = append(ads, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "restaurant_id": restaurantID, "ads": ads})
}

// handleAdsPluginJS serves GET /public/ads-plugin.js — the embeddable script.
// Usage on any external site:
//
//	<script src="https://<backend>/api/public/ads-plugin.js" data-restaurant="1" data-mode="overlay" async></script>
//
// data-mode: "overlay" (modal popover, default) or "inline" (injects the iframe
// where the script tag sits). Overlay close is remembered per ad for 24h.
func (s *Server) handleAdsPluginJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(w, adsPluginJS)
}

const adsPluginJS = `(function () {
  var script = document.currentScript || (function () {
    var s = document.getElementsByTagName("script");
    return s[s.length - 1];
  })();
  if (!script) return;
  var base = new URL(script.src).origin;
  var restaurant = script.getAttribute("data-restaurant") || "";
  if (!restaurant) return;
  var mode = script.getAttribute("data-mode") || "overlay";

  // Coord id ads_canvas_breakpoints_v1 - same thresholds as the home popover.
  function variantForWidth(vw) {
    if (vw > 0 && vw < 768) return "mobile";
    if (vw > 0 && vw < 1024) return "tablet";
    return "desktop";
  }
  function pickDims(ad) {
    var name = variantForWidth(window.innerWidth);
    if (ad.variants && ad.variants[name]) return ad.variants[name];
    return { width: ad.width, height: ad.height };
  }

  function safeLink(raw) {
    if (typeof raw !== "string" || !raw || raw.length > 2048) return null;
    if (raw.charAt(0) === "/" && raw.charAt(1) !== "/") return raw;
    try { var u = new URL(raw); if (["https:","http:","mailto:","tel:"].indexOf(u.protocol) >= 0) return raw; } catch(e) {}
    return null;
  }
  function inject(ad, container) {
    if (ad.steps && ad.steps.length) { injectWizard(ad, container); return; }
    var iframe = document.createElement("iframe");
    iframe.src = base + ad.render_url + "&variant=" + variantForWidth(window.innerWidth);
    iframe.setAttribute("title", ad.name || "Anuncio");
    iframe.style.border = "0";
    iframe.style.width = "100%";
    iframe.style.height = "100%";
    iframe.style.display = "block";
    iframe.setAttribute("loading", "lazy");
    container.appendChild(iframe);
  }

  function injectWizard(ad, container) {
    var body = document.createElement("div");
    body.style.cssText = "height:100%;overflow:auto;background:#fffdf8;color:#24342b;";
    container.appendChild(body);
    function buttons(list, target) {
      (list || []).forEach(function(cta){var href=safeLink(cta.navigation_mode==="custom"?cta.custom_url:cta.route);if(!href)return;var a=document.createElement("a");a.href=href;a.textContent=cta.text;a.style.cssText="display:block;margin:8px 0;padding:12px;border-radius:12px;background:#24342b;color:white;text-align:center;";target.appendChild(a);});
    }
    function cards() {
      body.replaceChildren(); body.style.padding = "22px";
      container.style.maxWidth=ad.width+"px"; container.style.aspectRatio=ad.width+"/"+ad.height;
      ad.steps.forEach(function(step) {
        var card = document.createElement("div"); card.style.cssText = "padding:16px;margin-bottom:12px;background:#eee4d0;border-radius:12px;";
        var title=document.createElement("h2");title.textContent=step.title;card.appendChild(title);
        var desc=document.createElement("p");desc.textContent=step.description;card.appendChild(desc);
        buttons(step.buttons,card);
        var open=document.createElement("button");open.type="button";open.textContent="Ver más";open.onclick=function(){detail(step);};card.appendChild(open);body.appendChild(card);
      });
    }
    function detail(step) {
      body.replaceChildren(); body.style.padding = step.canvas ? "0" : "22px";
      var back=document.createElement("button");back.type="button";back.textContent="Volver";back.onclick=cards;back.style.cssText="position:absolute;top:8px;left:8px;z-index:2;";body.appendChild(back);
      if(step.canvas) {
        var doc=step.canvas;var name=variantForWidth(window.innerWidth);var v=doc.variants && doc.variants[name];var dims=v || doc;
        var chrome=Object.assign({},doc.container || {},dims.container || {});
        container.style.maxWidth=(chrome.maxWidth || dims.width || doc.width)+"px";container.style.aspectRatio=(dims.width || doc.width)+"/"+(dims.height || doc.height);container.style.borderRadius=(chrome.radius===undefined?24:chrome.radius)+"px";
        var holder=document.createElement("div");holder.style.cssText="width:100%;aspect-ratio:"+(dims.width || doc.width)+"/"+(dims.height || doc.height)+";";body.appendChild(holder);
        inject({name:step.title,render_url:ad.render_url+"&step="+encodeURIComponent(step.id)},holder);
      } else {
        (step.content || []).forEach(function(item){
          var el=document.createElement(item.type==="image"?"img":item.type==="title"?"h2":item.type==="subtitle"?"h3":"p");
          if(item.type==="image"){var src=safeLink(item.value);if(!src || !/^https?:/.test(src))return;el.src=src;el.alt="";el.style.maxWidth="100%";}else el.textContent=item.value;
          body.appendChild(el);
        });
        buttons(ad.ctas,body);
      }
    }
    cards();
  }

  function showOverlay(ad) {
    var seenKey = "vc-ad-seen-" + ad.id;
    try {
      var seen = parseInt(localStorage.getItem(seenKey) || "0", 10);
      if (seen && Date.now() - seen < 24 * 3600 * 1000) return;
    } catch (e) {}
    var dims = pickDims(ad);
    var container = Object.assign({}, ad.container || {}, dims.container || {});
    var maxWidth = container.maxWidth || dims.width;
    var overlay = document.createElement("div");
    overlay.style.cssText = "position:fixed;inset:0;z-index:99999;background:" + (container.overlay || "rgba(0,0,0,.72)") + ";display:flex;align-items:center;justify-content:center;padding:18px;box-sizing:border-box;";
    var box = document.createElement("div");
    box.style.cssText = "position:relative;width:100%;max-width:" + Math.min(maxWidth, dims.width) + "px;aspect-ratio:" + dims.width + "/" + dims.height + ";max-height:86vh;border-radius:" + (container.radius === undefined ? 24 : container.radius) + "px;overflow:hidden;box-shadow:0 18px 60px rgba(0,0,0,.45);background:#fff;";
    var close = document.createElement("button");
    close.setAttribute("aria-label", "Cerrar anuncio");
    close.textContent = "\u00d7";
    close.style.cssText = "position:absolute;top:8px;right:8px;width:40px;height:40px;border:none;border-radius:50%;background:rgba(0,0,0,.55);color:#fff;font-size:22px;line-height:1;cursor:pointer;z-index:2;";
    close.addEventListener("click", function () {
      try { localStorage.setItem(seenKey, String(Date.now())); } catch (e) {}
      overlay.remove();
    });
    overlay.addEventListener("click", function (ev) { if (ev.target === overlay) close.click(); });
    box.appendChild(close);
    inject(ad, box);
    overlay.appendChild(box);
    document.body.appendChild(overlay);
  }

  fetch(base + "/api/public/ads/embed?restaurant_id=" + encodeURIComponent(restaurant))
    .then(function (r) { return r.json(); })
    .then(function (data) {
      if (!data || !data.ads || !data.ads.length) return;
      var ad = data.ads[0];
      if (mode === "inline") {
        var holder = document.createElement("div");
        var dimsInline = pickDims(ad);
        holder.style.cssText = "width:100%;max-width:" + dimsInline.width + "px;aspect-ratio:" + dimsInline.width + "/" + dimsInline.height + ";";
        script.parentNode.insertBefore(holder, script.nextSibling);
        inject(ad, holder);
      } else {
        if (document.readyState === "loading") {
          document.addEventListener("DOMContentLoaded", function () { showOverlay(ad); });
        } else {
          showOverlay(ad);
        }
      }
    })
    .catch(function () {});
})();
`
