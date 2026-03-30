package window

/*
#cgo pkg-config: gtk+-3.0
#cgo CFLAGS: -Wno-deprecated-declarations

#ifdef HAVE_GTK_LAYER_SHELL
#cgo pkg-config: gtk-layer-shell-0
#include <gtk-layer-shell.h>
#endif

#include <gtk/gtk.h>
#include <stdlib.h>
#include <string.h>

// Forward declarations for Go callbacks
extern void goActivate(void*);

static void on_activate(GtkApplication *app, gpointer user_data) {
    goActivate(user_data);
}

// Helper to connect the activate signal
static gulong connect_activate(GtkApplication *app, gpointer user_data) {
    return g_signal_connect(app, "activate", G_CALLBACK(on_activate), user_data);
}

// Create a CSS provider and load CSS
static GtkCssProvider* create_css_provider(const char *css) {
    GtkCssProvider *provider = gtk_css_provider_new();
    gtk_css_provider_load_from_data(provider, css, -1, NULL);
    gtk_style_context_add_provider_for_screen(
        gdk_screen_get_default(),
        GTK_STYLE_PROVIDER(provider),
        GTK_STYLE_PROVIDER_PRIORITY_APPLICATION
    );
    return provider;
}

// Setup layer shell for a window. Returns 1 if layer shell was used, 0 otherwise.
static int setup_layer_shell(GtkWindow *win) {
#ifdef HAVE_GTK_LAYER_SHELL
    if (gtk_layer_is_supported()) {
        gtk_layer_init_for_window(win);
        gtk_layer_set_layer(win, GTK_LAYER_SHELL_LAYER_OVERLAY);
        gtk_layer_set_keyboard_mode(win, GTK_LAYER_SHELL_KEYBOARD_MODE_NONE);

        // Anchor to top center
        gtk_layer_set_anchor(win, GTK_LAYER_SHELL_EDGE_TOP, TRUE);
        gtk_layer_set_anchor(win, GTK_LAYER_SHELL_EDGE_LEFT, FALSE);
        gtk_layer_set_anchor(win, GTK_LAYER_SHELL_EDGE_RIGHT, FALSE);
        gtk_layer_set_anchor(win, GTK_LAYER_SHELL_EDGE_BOTTOM, FALSE);

        gtk_layer_set_margin(win, GTK_LAYER_SHELL_EDGE_TOP, 48);
        return 1;
    }
#endif
    return 0;
}

// Fallback: position as a non-focusable floating window at top-center.
// GDK_WINDOW_TYPE_HINT_TOOLTIP is the most reliable way to prevent
// GNOME from giving focus to the window.
static void setup_fallback_window(GtkWindow *win) {
    gtk_window_set_type_hint(win, GDK_WINDOW_TYPE_HINT_TOOLTIP);
    gtk_window_set_keep_above(win, TRUE);
    gtk_window_set_skip_taskbar_hint(win, TRUE);
    gtk_window_set_skip_pager_hint(win, TRUE);
    gtk_window_set_accept_focus(win, FALSE);
    gtk_window_set_focus_on_map(win, FALSE);

    // Position at top-center of screen
    GdkScreen *screen = gtk_window_get_screen(win);
    int screen_width = gdk_screen_get_width(screen);
    int win_width = 600;
    gtk_window_move(win, (screen_width - win_width) / 2, 48);
}

// Thread-safe GTK operations via g_idle_add
typedef struct {
    GtkLabel *label;
    char *text;
} LabelUpdate;

typedef struct {
    GtkWidget *widget;
    int show; // 1 = show, 0 = hide
} VisibilityUpdate;

static gboolean idle_update_label(gpointer data) {
    LabelUpdate *u = (LabelUpdate*)data;
    if (u && u->label && GTK_IS_LABEL(u->label)) {
        gtk_label_set_text(u->label, u->text);
    }
    if (u) {
        free(u->text);
        free(u);
    }
    return G_SOURCE_REMOVE;
}

static gboolean idle_set_visibility(gpointer data) {
    VisibilityUpdate *u = (VisibilityUpdate*)data;
    if (u && u->widget && GTK_IS_WIDGET(u->widget)) {
        if (u->show) {
            gtk_widget_show_all(u->widget);
        } else {
            gtk_widget_hide(u->widget);
        }
    }
    free(u);
    return G_SOURCE_REMOVE;
}

static void schedule_label_update(GtkLabel *label, const char *text) {
    LabelUpdate *u = (LabelUpdate*)malloc(sizeof(LabelUpdate));
    u->label = label;
    u->text = strdup(text);
    g_idle_add(idle_update_label, u);
}

static void schedule_visibility(GtkWidget *widget, int show) {
    VisibilityUpdate *u = (VisibilityUpdate*)malloc(sizeof(VisibilityUpdate));
    u->widget = widget;
    u->show = show;
    g_idle_add(idle_set_visibility, u);
}

static gboolean idle_quit_app(gpointer data) {
    GApplication *app = (GApplication*)data;
    if (app) {
        g_application_quit(app);
    }
    return G_SOURCE_REMOVE;
}

static void schedule_quit(GApplication *app) {
    g_idle_add(idle_quit_app, app);
}
*/
import "C"
import (
	"sync"
	"unsafe"
)

const css = `
window {
    background-color: rgba(30, 30, 30, 0.92);
    border-radius: 12px;
    border: 1px solid rgba(255, 255, 255, 0.08);
}
.text-label {
    color: #e0e0e0;
    font-family: "Inter", "Cantarell", sans-serif;
    font-size: 16px;
    padding: 16px 24px;
}
.status-label {
    color: #888888;
    font-family: "Inter", "Cantarell", sans-serif;
    font-size: 12px;
    padding: 4px 24px 12px 24px;
}
`

// Overlay is the floating window that shows streaming transcription text.
type Overlay struct {
	mu      sync.Mutex
	app     *C.GtkApplication
	window  *C.GtkWidget
	textLbl *C.GtkLabel
	statLbl *C.GtkLabel
	ready   chan struct{}
}

// Global reference so the C callback can reach it.
var globalOverlay *Overlay

// New creates the overlay (call from main goroutine).
func New() *Overlay {
	o := &Overlay{
		ready: make(chan struct{}),
	}
	globalOverlay = o
	return o
}

// Run starts the GTK main loop. Blocks forever. Call from the main goroutine.
func (o *Overlay) Run() {
	appID := C.CString("com.sussurro.stream")
	defer C.free(unsafe.Pointer(appID))

	o.app = C.gtk_application_new(appID, C.G_APPLICATION_DEFAULT_FLAGS)
	C.connect_activate(o.app, nil)

	// Run the GTK application
	C.g_application_run((*C.GApplication)(unsafe.Pointer(o.app)), 0, nil)
	C.g_object_unref(C.gpointer(unsafe.Pointer(o.app)))
}

// Wait blocks until the window is created and ready.
func (o *Overlay) Wait() {
	<-o.ready
}

// SetText updates the main text label (thread-safe).
func (o *Overlay) SetText(text string) {
	o.mu.Lock()
	lbl := o.textLbl
	o.mu.Unlock()

	if lbl == nil {
		return
	}

	cs := C.CString(text)
	defer C.free(unsafe.Pointer(cs))
	C.schedule_label_update(lbl, cs)
}

// SetStatus updates the status label (thread-safe).
func (o *Overlay) SetStatus(text string) {
	o.mu.Lock()
	lbl := o.statLbl
	o.mu.Unlock()

	if lbl == nil {
		return
	}

	cs := C.CString(text)
	defer C.free(unsafe.Pointer(cs))
	C.schedule_label_update(lbl, cs)
}

// Show makes the window visible (thread-safe).
func (o *Overlay) Show() {
	o.mu.Lock()
	win := o.window
	o.mu.Unlock()

	if win == nil {
		return
	}
	C.schedule_visibility(win, 1)
}

// Hide hides the window (thread-safe).
func (o *Overlay) Hide() {
	o.mu.Lock()
	win := o.window
	o.mu.Unlock()

	if win == nil {
		return
	}
	C.schedule_visibility(win, 0)
}

// Quit terminates the GTK main loop (thread-safe).
func (o *Overlay) Quit() {
	if o.app == nil {
		return
	}
	C.schedule_quit((*C.GApplication)(unsafe.Pointer(o.app)))
}

//export goActivate
func goActivate(userData unsafe.Pointer) {
	o := globalOverlay

	// Apply CSS
	cssStr := C.CString(css)
	defer C.free(unsafe.Pointer(cssStr))
	C.create_css_provider(cssStr)

	// Create window
	win := C.gtk_application_window_new(o.app)
	window := (*C.GtkWindow)(unsafe.Pointer(win))

	C.gtk_window_set_title(window, C.CString("sussurro-stream"))
	C.gtk_window_set_default_size(window, 600, -1)
	C.gtk_window_set_resizable(window, C.FALSE)
	C.gtk_window_set_decorated(window, C.FALSE)

	// Layer shell setup (no focus steal, overlay layer), with fallback
	if C.setup_layer_shell(window) == 0 {
		C.setup_fallback_window(window)
	}

	// Vertical box layout
	vbox := C.gtk_box_new(C.GTK_ORIENTATION_VERTICAL, 0)
	C.gtk_container_add((*C.GtkContainer)(unsafe.Pointer(win)), vbox)

	// Text label
	textLbl := C.gtk_label_new(C.CString(""))
	C.gtk_label_set_line_wrap((*C.GtkLabel)(unsafe.Pointer(textLbl)), C.TRUE)
	C.gtk_label_set_max_width_chars((*C.GtkLabel)(unsafe.Pointer(textLbl)), 60)
	C.gtk_label_set_xalign((*C.GtkLabel)(unsafe.Pointer(textLbl)), 0)

	textCtx := C.gtk_widget_get_style_context(textLbl)
	textClass := C.CString("text-label")
	defer C.free(unsafe.Pointer(textClass))
	C.gtk_style_context_add_class(textCtx, textClass)

	C.gtk_box_pack_start((*C.GtkBox)(unsafe.Pointer(vbox)), textLbl, C.TRUE, C.TRUE, 0)

	// Status label
	statLbl := C.gtk_label_new(C.CString(""))
	C.gtk_label_set_xalign((*C.GtkLabel)(unsafe.Pointer(statLbl)), 0)

	statCtx := C.gtk_widget_get_style_context(statLbl)
	statClass := C.CString("status-label")
	defer C.free(unsafe.Pointer(statClass))
	C.gtk_style_context_add_class(statCtx, statClass)

	C.gtk_box_pack_start((*C.GtkBox)(unsafe.Pointer(vbox)), statLbl, C.FALSE, C.FALSE, 0)

	o.mu.Lock()
	o.window = win
	o.textLbl = (*C.GtkLabel)(unsafe.Pointer(textLbl))
	o.statLbl = (*C.GtkLabel)(unsafe.Pointer(statLbl))
	o.mu.Unlock()

	// Start hidden
	C.gtk_widget_show_all(win)
	C.gtk_widget_hide(win)

	close(o.ready)
}
