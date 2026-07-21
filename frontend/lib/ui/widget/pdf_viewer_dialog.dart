import 'package:flutter/material.dart';
import 'package:pdfrx/pdfrx.dart';

import '../../model/course.dart';
import '../../service/api_service.dart';
import 'glass_panel.dart';

/// Modal PDF viewer dialog used by the player's attachment panel.
///
/// NOTE: bypasses ApiService because PDF streams arbitrary book URLs with
/// manual byte counting (the Go backend's 302 attachment endpoint issues
/// range requests directly against the storage URL).
class PdfViewerDialog {
  PdfViewerDialog._();

  static void show(BuildContext context, Attachment att, String url) {
    showDialog(
      context: context,
      barrierColor: const Color(0x900F172A),
      builder: (context) {
        return Center(
          child: Container(
            constraints: const BoxConstraints(maxWidth: 900),
            height: MediaQuery.of(context).size.height * 0.85,
            child: GlassPanel(
              borderRadius: 24,
              baseColor: Colors.white,
              borderColor: Colors.white,
              borderWidth: 2,
              padding: const EdgeInsets.all(0),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Container(
                    padding: const EdgeInsets.all(16),
                    decoration: const BoxDecoration(
                      color: Color(0xFFFFF7ED),
                      borderRadius: BorderRadius.only(
                        topLeft: Radius.circular(22),
                        topRight: Radius.circular(22),
                      ),
                    ),
                    child: Row(
                      children: [
                        const Icon(Icons.picture_as_pdf_rounded,
                            color: Color(0xFFF97316)),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Text(att.fileName,
                              style: const TextStyle(
                                  fontWeight: FontWeight.w900,
                                  color: Color(0xFF7C2D12))),
                        ),
                        IconButton(
                          onPressed: () => Navigator.pop(context),
                          icon: const Icon(Icons.close_rounded),
                        ),
                      ],
                    ),
                  ),
                  Expanded(
                    child: PdfViewer(
                      PdfDocumentRefUri(
                        Uri.parse(url),
                        // Auth via the opaque session token (legacy X-User-ID
                        // is rejected by the backend). Empty if logged out;
                        // the request then 401s instead of using a dead identity.
                        headers: {
                          if (ApiService.authToken != null &&
                              ApiService.authToken!.isNotEmpty)
                            'Authorization':
                                'Bearer ${ApiService.authToken}',
                        },
                      ),
                      params: PdfViewerParams(
                        // Keep the viewer ready for streaming range requests
                        // from the Go backend's 302 attachment endpoint.
                        enableTextSelection: true,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}
