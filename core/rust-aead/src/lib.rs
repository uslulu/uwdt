use ring::aead::{Aad, LessSafeKey, Nonce, Tag, UnboundKey, CHACHA20_POLY1305};
use std::{ptr, slice};

// Callers own the key and keep it alive until both packet loops finish.
// Packet buffers and their headers must be disjoint. Rust borrows them only
// for the duration of the call.
#[no_mangle]
pub unsafe extern "C" fn uwdt_aead_new(key: *const u8) -> *mut LessSafeKey {
    let key = slice::from_raw_parts(key, 32);
    match UnboundKey::new(&CHACHA20_POLY1305, key) {
        Ok(key) => Box::into_raw(Box::new(LessSafeKey::new(key))),
        Err(_) => ptr::null_mut(),
    }
}

#[no_mangle]
pub unsafe extern "C" fn uwdt_aead_free(key: *mut LessSafeKey) {
    drop(Box::from_raw(key));
}

#[no_mangle]
pub unsafe extern "C" fn uwdt_aead_seal(
    key: *const LessSafeKey,
    nonce: *const [u8; 12],
    header: *const [u8; 12],
    packet: *mut u8,
    length: usize,
) -> bool {
    let packet = slice::from_raw_parts_mut(packet, length + 16);
    let (body, tag_out) = packet.split_at_mut(length);
    let Ok(tag) = (*key).seal_in_place_separate_tag(
        Nonce::assume_unique_for_key(*nonce),
        Aad::from(&*header),
        body,
    ) else {
        return false;
    };
    tag_out.copy_from_slice(tag.as_ref());
    true
}

#[no_mangle]
pub unsafe extern "C" fn uwdt_aead_open(
    key: *const LessSafeKey,
    nonce: *const [u8; 12],
    header: *const [u8; 12],
    packet: *mut u8,
    length: usize,
    tag: *const [u8; 16],
) -> bool {
    let packet = slice::from_raw_parts_mut(packet, length);
    (*key).open_in_place_separate_tag(
        Nonce::assume_unique_for_key(*nonce),
        Aad::from(&*header),
        Tag::from(*tag),
        packet,
        0..,
    ).is_ok()
}
