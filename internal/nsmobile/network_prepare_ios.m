//go:build ios && cgo

#import <Foundation/Foundation.h>
#include <stdatomic.h>

@interface MiyaNetworkPreparation : NSObject {
@public
    atomic_int result;
}
@property(nonatomic, strong) NSURLSession *session;
@property(nonatomic, strong) NSURLSessionDataTask *task;
@end
@implementation MiyaNetworkPreparation
- (void)dealloc {
    [_task release];
    [_session release];
    [super dealloc];
}
@end

void *miya_network_start(const char *rawURL) {
    @autoreleasepool {
        NSURL *url = [NSURL URLWithString:[NSString stringWithUTF8String:rawURL]];
        if (!url || ![url.scheme.lowercaseString isEqualToString:@"https"] || !url.host.length) return NULL;
        MiyaNetworkPreparation *request = [[MiyaNetworkPreparation alloc] init];
        atomic_init(&request->result, 0);
        NSURLSessionConfiguration *config = [NSURLSessionConfiguration ephemeralSessionConfiguration];
        config.URLCache = nil;
        config.timeoutIntervalForRequest = 15;
        config.timeoutIntervalForResource = 20;
        request.session = [NSURLSession sessionWithConfiguration:config];
        NSMutableURLRequest *head = [NSMutableURLRequest requestWithURL:url];
        head.HTTPMethod = @"HEAD";
        request.task = [request.session dataTaskWithRequest:head completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
            atomic_store(&request->result, response && !error ? 1 : -1);
        }];
        [request.task resume];
        return request;
    }
}

int miya_network_result(void *handle) {
    return atomic_load(&((MiyaNetworkPreparation *)handle)->result);
}

void miya_network_close(void *handle) {
    @autoreleasepool {
        MiyaNetworkPreparation *request = (MiyaNetworkPreparation *)handle;
        [request.task cancel];
        [request.session invalidateAndCancel];
        [request release];
    }
}
