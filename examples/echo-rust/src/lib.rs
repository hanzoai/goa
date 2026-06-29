//! A goa Rust guest. It implements the goa string ABI so a Rust service runs
//! in-process under wazero (pure-Go host, no cgo). No std, no external crates:
//! a tiny bump allocator over a static arena backs alloc(), and a live-count in
//! dealloc() resets the arena once every outstanding buffer is freed — so
//! repeated calls never grow memory (the host frees both the payload and the
//! result each invoke).
//!
//!   alloc(len) -> ptr        host's buffer for the JSON payload
//!   dealloc(ptr, len)        frees; resets the arena when nothing is live
//!   <fn>(ptr, len) -> i64    returns (resultPtr << 32 | resultLen)
//!
//! A real service would swap the arena for std's allocator (build with the
//! wasm32 std) or wee_alloc; the ABI surface is identical. The `#[goa::handler]`
//! proc-macro will generate alloc/dealloc/pack so a service author writes only
//! `fn echo(input: &[u8]) -> Vec<u8>`.
#![no_std]

use core::panic::PanicInfo;
use core::ptr::addr_of_mut;
use core::slice;

#[panic_handler]
fn panic(_: &PanicInfo) -> ! {
    loop {}
}

const ARENA: usize = 1 << 16; // 64 KiB
static mut HEAP: [u8; ARENA] = [0; ARENA];
static mut TOP: usize = 0;
static mut LIVE: i32 = 0;

#[no_mangle]
pub extern "C" fn alloc(len: i32) -> i32 {
    unsafe {
        let base = addr_of_mut!(HEAP) as usize;
        let off = TOP;
        TOP += len as usize;
        LIVE += 1;
        (base + off) as i32
    }
}

#[no_mangle]
pub extern "C" fn dealloc(_ptr: i32, _len: i32) {
    unsafe {
        LIVE -= 1;
        if LIVE <= 0 {
            LIVE = 0;
            TOP = 0;
        }
    }
}

#[inline]
fn pack(ptr: i32, len: i32) -> i64 {
    ((ptr as i64) << 32) | ((len as i64) & 0xffff_ffff)
}

/// echo(payload) -> {"echo": <payload>} — wraps the incoming JSON value.
#[no_mangle]
pub extern "C" fn echo(ptr: i32, len: i32) -> i64 {
    let input = unsafe { slice::from_raw_parts(ptr as *const u8, len as usize) };
    const PREFIX: &[u8] = b"{\"echo\":";
    let total = PREFIX.len() + input.len() + 1;
    let out = alloc(total as i32);
    let dst = unsafe { slice::from_raw_parts_mut(out as *mut u8, total) };
    dst[..PREFIX.len()].copy_from_slice(PREFIX);
    dst[PREFIX.len()..PREFIX.len() + input.len()].copy_from_slice(input);
    dst[total - 1] = b'}';
    pack(out, total as i32)
}
