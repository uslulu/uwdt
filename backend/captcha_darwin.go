package backend

// Окно ручной капчи ВК (macOS). Запускается отдельным процессом:
// `<бинарь> --captcha-window <url>` — у Wails свой цикл событий, второе окно
// в том же процессе с ним бы спорило. Человек решает капчу на странице ВК,
// страница сама получает success_token от API ВК — скрипт ниже только
// пересылает его в приложение. Токен печатается в stdout, процесс выходит.

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

@interface UWDTCaptcha : NSObject <WKScriptMessageHandler, NSWindowDelegate>
@end

@implementation UWDTCaptcha
- (void)userContentController:(WKUserContentController *)ucc didReceiveScriptMessage:(WKScriptMessage *)msg {
	if ([msg.body isKindOfClass:[NSString class]] && [(NSString *)msg.body length] > 0) {
		printf("TOKEN %s\n", [(NSString *)msg.body UTF8String]);
		fflush(stdout);
		[NSApp terminate:nil];
	}
}
- (void)windowWillClose:(NSNotification *)n {
	printf("CANCEL\n");
	fflush(stdout);
	[NSApp terminate:nil];
}
@end

static void uwdtRunCaptcha(const char *curl, const char *cjs, const char *ctitle) {
	@autoreleasepool {
		[NSApplication sharedApplication];
		[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];

		UWDTCaptcha *handler = [UWDTCaptcha new];
		WKWebViewConfiguration *cfg = [WKWebViewConfiguration new];
		WKUserScript *script = [[WKUserScript alloc] initWithSource:[NSString stringWithUTF8String:cjs]
		                                              injectionTime:WKUserScriptInjectionTimeAtDocumentStart
		                                           forMainFrameOnly:NO];
		[cfg.userContentController addUserScript:script];
		[cfg.userContentController addScriptMessageHandler:handler name:@"uwdt"];

		NSRect frame = NSMakeRect(0, 0, 440, 620);
		NSWindow *win = [[NSWindow alloc] initWithContentRect:frame
		                                            styleMask:(NSWindowStyleMaskTitled | NSWindowStyleMaskClosable)
		                                              backing:NSBackingStoreBuffered
		                                                defer:NO];
		win.title = [NSString stringWithUTF8String:ctitle];
		win.delegate = handler;
		win.releasedWhenClosed = NO;

		WKWebView *web = [[WKWebView alloc] initWithFrame:frame configuration:cfg];
		win.contentView = web;
		[web loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:[NSString stringWithUTF8String:curl]]]];

		[win center];
		[win setLevel:NSFloatingWindowLevel];
		[win makeKeyAndOrderFront:nil];
		[NSApp activateIgnoringOtherApps:YES];
		[NSApp run];
	}
}
*/
import "C"

import (
	"fmt"
	"net/url"
	"os"
	"unsafe"
)

// RunCaptchaWindowDarwin — точка входа режима --captcha-window (вызывается из main_darwin.go).
func RunCaptchaWindowDarwin(args []string) {
	if len(args) < 1 {
		os.Exit(2)
	}
	u, err := url.Parse(args[0])
	if err != nil || u.Scheme != "https" || !isVKCaptchaHost(u.Hostname()) {
		fmt.Println("CANCEL")
		os.Exit(2)
	}
	curl := C.CString(u.String())
	cjs := C.CString(captchaBridgeJS("window.webkit.messageHandlers.uwdt.postMessage(t)"))
	ctitle := C.CString("Капча ВКонтакте — UWDT")
	defer C.free(unsafe.Pointer(curl))
	defer C.free(unsafe.Pointer(cjs))
	defer C.free(unsafe.Pointer(ctitle))
	C.uwdtRunCaptcha(curl, cjs, ctitle)
}
